package main

import (
    "os"
    "io"
    "bufio"
    "fmt"
    "io/ioutil"
    "github.com/docopt/docopt-go"
    "github.com/smallfish/simpleyaml"
    "github.com/awslabs/aws-sdk-go/aws"
    "github.com/awslabs/aws-sdk-go/aws/awsutil"
    "github.com/awslabs/aws-sdk-go/service/s3"
)

// StorageConfiguration is the object containing the details used in deployment.
// It is passed to methods that are in need of instruction.
type StorageConfiguration struct {
    applicationName    string  // name of application being deployed
    deploymentLocation string  // were we end up putting versions of app
}

func main() {
    usage := `

    Usage:
        s3_deployer install <version>

    Options:
        -h --help    Show this screen`

    arguments, _ := docopt.Parse(usage, nil, true, "S3 Deployer 0.1.0", false)
    fmt.Println(arguments)

    config := &StorageConfiguration{
        applicationName: "legoland",
        deploymentLocation: "/var/pd-hg-assets/assets/",
    }
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
    //fmt.Printf("%s %s\n", access_key_id, secret_access_key)

    creds := aws.Creds(access_key_id, secret_access_key, "")
    s3_svc := s3.New(&aws.Config{Region: "us-west-1", Credentials: creds})

    //list_buckets(s3_svc)
    key := "legoland/legoland-09924950.zip" // should be passed as arg
    err := InstallArtifact(s3_svc, key)
    if awserr := aws.Error(err); awserr != nil {
        if awserr.Code == "NoSuchKey" {
            fmt.Printf("The key '%s' does not exist in S3 repository!\n", key)
        } else {
            fmt.Println("Error:", awserr.Code, awserr.Message)
        }
    } else if err != nil {
        return err
    }
}

func list_buckets(s3_svc *s3.S3) {
    params := &s3.ListObjectsInput{
        Bucket: aws.String("pd-release"),
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
// error may be aws.Error
func InstallArtifact(s3_svc *s3.S3, s3Key string, installLocation string) error {
    // check if file already exists on machine, if so do nothing

    // download the artifact, unzip it to install location
    artifact, err := GetArtifact(s3_svc, s3Key)

    // write out the S3 artifact, ensure stream is closed on method exit
    WriteOut(artifact.Body, installLocation)

    if err := artifact.Body.Close(); err != nil {
        return err
    }
}

// GetArtifact downloads the requested zip archive file from S3.
// Returns the s3.GetObjectOutput object representing this file, or error.
func GetArtifact(s3_svc *s3.S3, key string) (*s3.GetObjectOutput, error) {
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

// WriteOut will take the given Reader interface and stream it's contents to the given file. 
// Will return error if unable to write file or issues flushing/closing writer.
func WriteOut(reader io.ReadCloser, writeToLocation string) error {
    // setup file to write out to
    location := fmt.Sprintf("/var/pd-hg-assets/assets/%s", filename

    // create or overwrite file
    fo, err := os.Create(location)
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
            panic(err)
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
