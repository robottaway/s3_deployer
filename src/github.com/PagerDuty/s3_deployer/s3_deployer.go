package main

import (
	"archive/zip"
	"bufio"
	"fmt"
	"github.com/awslabs/aws-sdk-go/aws"
	"github.com/awslabs/aws-sdk-go/aws/awsutil"
	"github.com/awslabs/aws-sdk-go/service/s3"
	"github.com/docopt/docopt-go"
	"github.com/smallfish/simpleyaml"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// StorageConfiguration is the object containing the details used in deployment.
// It is passed to methods that are in need of instruction.
type StorageConfiguration struct {
	applicationName    string // name of application being deployed
	deploymentLocation string // were we end up putting versions of app
}

func main() {
	usage := `

    Usage:
        s3_deployer install <version>

    Options:
        -h --help    Show this screen`

	arguments, _ := docopt.Parse(usage, nil, true, "S3 Deployer 0.1.0", false)
	fmt.Println(arguments)

	content, err := ioutil.ReadFile("/etc/pagerduty/devtools.yml")
	if err != nil {
		panic(err)
	}
	yaml, err := simpleyaml.NewYaml(content)
	if err != nil {
		panic(err)
	}
	access_key_id, err := yaml.Get("access_key_id").String()
	secret_access_key, err := yaml.Get("secret_access_key").String()

	creds := aws.Creds(access_key_id, secret_access_key, "")
	s3_config := &aws.Config{Region: "us-west-1", Credentials: creds}
	s3_svc := s3.New(s3_config)

	//list_buckets(s3_svc)
	key := "legoland/legoland-09924950.zip" // should be passed as arg
	err = InstallArtifact(s3_svc, key, "/var/pd-hg-assets/assets", "junk.zip")
	if awserr := aws.Error(err); awserr != nil {
		if awserr.Code == "NoSuchKey" {
			fmt.Printf("The key '%s' does not exist in S3 repository!\n", key)
		} else {
			fmt.Println("Error:", awserr.Code, awserr.Message)
		}
	} else if err != nil {
		panic(err)
	}
}

// ListBucket returns listing of contents of given bucket.
// Will print the raw JSON structure returned by S3 to stdout.
func ListBucket(s3_svc *s3.S3, bucketName string) {
	params := &s3.ListObjectsInput{
		Bucket: aws.String(bucketName),
	}
	resp, err := s3_svc.ListObjects(params)
	if awserr := aws.Error(err); awserr != nil {
		fmt.Println("Error:", awserr.Code, awserr.Message)
	} else if err != nil {
		panic(err)
	}
	fmt.Println(awsutil.StringValue(resp))
}

// InstallArtifact will download and install given artifact.
// Will return error if unable to download or install.
// Be aware error may be aws.Error
func InstallArtifact(s3_svc *s3.S3, s3Key, installLocation, filename string) error {
	// check if file already exists on machine, if so do nothing

	// download the artifact, unzip it to install location
	artifact, err := DownloadArtifact(s3_svc, s3Key)
	if err != nil {
		return err
	}

	artifactLocation := fmt.Sprintf("%s/%s", installLocation, filename)

	// write out the S3 artifact, ensure stream is closed on method exit
	err = WriteOutArtifact(artifact.Body, artifactLocation)
	if err != nil {
		return err
	}

	if err = artifact.Body.Close(); err != nil {
		return err
	}

	// explode the contents of artifact
	err = UnzipArtifact(artifactLocation, "/usr/local/legoland/releases/junk")
	if err != nil {
		panic(err)
	}

	return nil
}

// GetArtifact downloads the requested zip archive file from S3.
// Returns the s3.GetObjectOutput object representing this file, or error.
func DownloadArtifact(s3_svc *s3.S3, key string) (*s3.GetObjectOutput, error) {
	// query S3 for the artifact being asked for
	get_obj_params := &s3.GetObjectInput{
		Bucket: aws.String("pd-release"),
		Key:    aws.String(key),
	}
	resp, err := s3_svc.GetObject(get_obj_params)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// WriteOutArtifact will take the given Reader interface and stream it's contents to the given file.
// Will return error if unable to write file or issues flushing/closing writer.
func WriteOutArtifact(reader io.ReadCloser, artifactLocation string) error {
	// create or overwrite file
	fo, err := os.Create(artifactLocation)
	if err != nil {
		return err
	}

	w := bufio.NewWriter(fo)
	buf := make([]byte, 1024)
	for {
		n, err := reader.Read(buf)
		if err != nil && err != io.EOF {
			return err
		}
		if n == 0 {
			break
		}
		if _, err := w.Write(buf[:n]); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if err := fo.Close(); err != nil {
		return err
	}
	return nil
}

// Unzip opens given src zip file and dumps it's contents in dest.
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

// FinalizePermissions  updates permissions on the files unzipped from artifact
func FinalizePermissions() {
	// run("chmod -R g+w #{lr}") if fetch(:group_writable, true
	// run("chmod u+x #{init_script(lr)}"):
	//     path + "/scripts/" + fetch(:application) + ".sh"
	// run("chmod u+x #{ping_script(lr)}"):
	//     path + "/scripts/ping.sh"
	// for each script run("chmod u+x #{get_script_path(script, lr)}")
}
