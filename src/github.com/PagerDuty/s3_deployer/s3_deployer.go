package main

import (
	"archive/zip"
	"bufio"
	"fmt"
	"github.com/awslabs/aws-sdk-go/aws"
	"github.com/awslabs/aws-sdk-go/service/s3"
	"github.com/docopt/docopt-go"
	"github.com/smallfish/simpleyaml"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const groupWriteMask os.FileMode = os.FileMode(0020)
const userExecuteMask os.FileMode = os.FileMode(0100)
const otherNoneMask os.FileMode = os.FileMode(0770)

// StorageConfiguration is the object containing the details used in deployment.
// It is passed to methods that are in need of instruction.
type InstallConfiguration struct {
	applicationName    string // name of application being deployed
	commitHash         string // the Git sha of the version being released
	deploymentLocation string // were we end up putting versions of apps we deploy
	releaseLocation    string // were given release will go (subfolder of deploymentLocation)
	scripts            string // a comma separated list of scripts, eg bin/one.sh,cmd/two.rb
	groupWritable      bool   // if true g+w files in deployment
	removeOther        bool   // if true o-rwx for files
}

func main() {
	usage := `S3 Deployer - installs archived applications from S3

    Usage:
        s3_deployer listbucket [--bucket=<bucketname>] [--matching=<matcher>]
        s3_deployer install <application> <version> [--scripts=<scriptLocations>]
		    [--groupwritable] [--removeother]
        s3_deployer -h | --help

    Options:
        -h --help                    Show this screen.
        --bucket=<bucketname>        Name of bucket to target [default: pd-release].
		--scripts=<scriptLocations>  Comma separated list of scripts relative to root of distribution.
        --matching=<matcher>         A regex to use in matching keys in bucket
		--groupwritable              If flag set will set all deployed files to g+w
		--removeother                Remove any permissions for other`

	arguments, _ := docopt.Parse(usage, nil, true, "S3 Deployer 0.1.0", false)

	content, err := ioutil.ReadFile("/etc/pagerduty/devtools.yml")
	if err != nil {
		panic(err)
	}

	// read the devtools configuration file
	yaml, err := simpleyaml.NewYaml(content)
	if err != nil {
		panic(err)
	}
	access_key_id, err := yaml.Get("access_key_id").String()
	secret_access_key, err := yaml.Get("secret_access_key").String()

	// build a S3 client
	creds := aws.Creds(access_key_id, secret_access_key, "")
	s3_config := &aws.Config{Region: "us-west-1", Credentials: creds}
	s3_svc := s3.New(s3_config)

	if arguments["listbucket"] == true {
		bucket, _ := arguments["--bucket"].(string)
		matcher, _ := arguments["--matching"].(string)
		ListBucket(s3_svc, bucket, matcher)
	} else if arguments["install"] == true {
		conf := BuildInstallConfig(arguments)
		err = InstallArtifact(s3_svc, conf)
		if awserr := aws.Error(err); awserr != nil {
			if awserr.Code == "NoSuchKey" {
				fmt.Printf(
					"Unable to find artifact for application '%s' commit hash '%s' in S3 repository!\n",
					conf.applicationName, conf.commitHash)
			} else {
				fmt.Println("Error:", awserr.Code, awserr.Message)
			}
		} else if err != nil {
			panic(err)
		}
	}
}

// BuildInstallConfig takes the arguments and returns a config object
func BuildInstallConfig(arguments map[string]interface{}) *InstallConfiguration {
	applicationName, _ := arguments["<application>"].(string)
	commitHash, _ := arguments["<version>"].(string)
	scripts, _ := arguments["--scripts"].(string)
	groupWritable, _ := arguments["--groupwritable"].(bool)
	removeOther, _ := arguments["--removeother"].(bool)
	deploymentLocation := "/usr/local/legoland"
	conf := &InstallConfiguration{
		applicationName:    applicationName,
		commitHash:         commitHash,
		deploymentLocation: deploymentLocation,
		releaseLocation:    fmt.Sprintf("%s/releases/%s", deploymentLocation, commitHash),
		scripts:            scripts,
		groupWritable:      groupWritable,
		removeOther:        removeOther,
	}
	return conf
}

// ListBucket returns listing of contents of given bucket.
// Will print the raw JSON structure returned by S3 to stdout.
func ListBucket(s3_svc *s3.S3, bucketName, matcher string) {
	params := &s3.ListObjectsInput{
		Bucket: aws.String(bucketName),
	}
	resp, err := s3_svc.ListObjects(params)
	if awserr := aws.Error(err); awserr != nil {
		if awserr.Code == "AccessDenied" {
			fmt.Printf("You do not have access to the bucket '%s'\n", bucketName)
		} else if awserr.Code == "NoSuchBucket" {
			fmt.Printf("Bucket '%s' not found\n", bucketName)
		} else {
			fmt.Println("S3 returned an error:", awserr.Code, awserr.Message)
		}
		return
	} else if err != nil {
		panic(err)
	}
	var r *regexp.Regexp = nil
	if matcher != "" {
		r, err = regexp.Compile(matcher)
		if err != nil {
			fmt.Printf("Matcher '%s' cannot be understood, should be RE2 type regex\n", matcher)
			return
		}
	}
	for _, value := range resp.Contents {
		if r != nil {
			if r.MatchString(*value.Key) {
				fmt.Println(*value.Key)
			}
		} else {
			fmt.Println(*value.Key)
		}
	}
}

// InstallArtifact will download and install given artifact.
// Will return error if unable to download or install.
// Be aware error may be aws.Error
func InstallArtifact(s3_svc *s3.S3, conf *InstallConfiguration) error {
	// check if file already exists on machine, if so do nothing
	fi, _ := os.Stat(conf.releaseLocation)
	if fi != nil {
		fmt.Printf(
			"Location '%s' already exists! Release '%s' already installed?\n",
			conf.releaseLocation, conf.commitHash)
		return nil
	}

	// download the artifact, unzip it to install location
	artifactLocation, err := DownloadArtifact(s3_svc, conf.applicationName, conf.commitHash)
	if err != nil {
		return err
	}

	// explode the contents of artifact
	err = UnzipArtifact(artifactLocation, conf.releaseLocation)
	if err != nil {
		panic(err)
	}

	FinalizePermissions(conf)

	return nil
}

// DownloadArtifact downloads the matching zip archive file from S3.
// Returns the s3.GetObjectOutput object representing this file, or error.
func DownloadArtifact(s3_svc *s3.S3, applicationName, commitHash string) (string, error) {
	key := fmt.Sprintf("%s/%s-%s.zip", applicationName, applicationName, commitHash)

	// query S3 for the artifact being asked for
	get_obj_params := &s3.GetObjectInput{
		Bucket: aws.String("pd-release"),
		Key:    aws.String(key),
	}
	resp, err := s3_svc.GetObject(get_obj_params)
	if err != nil {
		return "", err
	}

	prefix := fmt.Sprintf("s3_deployer-%s-%s", applicationName, commitHash)
	fo, err := ioutil.TempFile(os.TempDir(), prefix)
	if err != nil {
		return "", err
	}

	w := bufio.NewWriter(fo)
	buf := make([]byte, 1024)
	for {
		n, err := resp.Body.Read(buf)
		if err != nil && err != io.EOF {
			return "", err
		}
		if n == 0 {
			break
		}
		if _, err := w.Write(buf[:n]); err != nil {
			return "", err
		}
	}

	if err = resp.Body.Close(); err != nil {
		return "", err
	}

	if err := w.Flush(); err != nil {
		return "", err
	}

	if err := fo.Close(); err != nil {
		return "", err
	}

	return fo.Name(), nil
}

// UnzipArtifact opens given src zip file and dumps it's contents in dest.
func UnzipArtifact(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()

		fpath := filepath.Join(dest, f.Name)
		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, 0755)
		} else {
			var fdir string
			if lastIndex := strings.LastIndex(fpath, string(os.PathSeparator)); lastIndex > -1 {
				fdir = fpath[:lastIndex]
			}

			err = os.MkdirAll(fdir, 0755)
			if err != nil {
				log.Fatal(err)
				return err
			}
			f, err := os.OpenFile(
				fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
			if err != nil {
				return err
			}
			defer f.Close()

			_, err = io.Copy(f, rc)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// FinalizePermissions updates permissions on the files unzipped from artifact
func FinalizePermissions(conf *InstallConfiguration) {
	fmt.Printf("Updating any permissions as needed for files under '%s'\n", conf.releaseLocation)

	filepath.Walk(conf.releaseLocation, func(path string, info os.FileInfo, err error) error {
		perms := info.Mode()

		// make files group writable if requested
		if conf.groupWritable {
			perms |= groupWriteMask
		}
		// remove all other perms if requested
		if conf.removeOther {
			perms = perms & otherNoneMask
		}
		// make any scripts executable
		if strings.HasSuffix(path, ".sh") {
			perms |= userExecuteMask
		}
		// apply permission changes
		fm := os.FileMode(perms)
		os.Chmod(path, fm)

		return nil
	})

	if conf.scripts != "" {
		fmt.Println("Adding u+x for given scripts:")
		parts := strings.Split(conf.scripts, ",")
		for idx, val := range parts {
			parts[idx] = strings.Trim(val, " ")
			fullPath := fmt.Sprintf("%s/%s", conf.releaseLocation, parts[idx])
			fi, _ := os.Stat(fullPath)
			if fi != nil {
				var mode os.FileMode = fi.Mode() | os.FileMode(userExecuteMask)
				os.Chmod(fullPath, mode)
			} else {
				fmt.Printf("Unable to update permissions for script '%s', does it exist?\n", fullPath)
			}
		}
	}
}
