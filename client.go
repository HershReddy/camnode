/*
Copyright 2013 Google Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Binary storage-sample creates a new bucket, performs all of its operations
// within that bucket, and then cleans up after itself if nothing fails along the way.

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"code.google.com/p/goauth2/oauth"
	"code.google.com/p/google-api-go-client/storage/v1beta2"

	"github.com/BurntSushi/toml"
)

type AppConfig struct {
	ClientId     string `toml:"clientId"`
	ClientSecret string `toml:"clientSecret"`
	Oauth_code   string `toml:"oauth_code"`
}

type CheckStatusMessage struct {
	NewPicRequested bool
}

type UpdateServerMessage struct {
	LatestImageURL string
}

const (
	// Change these variable to match your personal information.
	bucketName         = "pipark2014"
	projectID          = "pipark2014"
	server_host        = "www.pipark2014.appspot.com"
	server_check_path  = "/clientcheck"
	server_update_path = "/clientupdate"
	location_name      = "300ThirdStreet"

	fileName       = "./test.jpg"                               // The name of the local file to upload.
	objectPath     = "parkingspots/imgs/" + location_name + "/" // This can be changed to any valid object path.
	sleep_duration = time.Second * 10                           // client pings server once in sleep_duration

	// For the basic sample, these variables need not be changed.
	scope       = storage.DevstorageFull_controlScope
	authURL     = "https://accounts.google.com/o/oauth2/auth"
	tokenURL    = "https://accounts.google.com/o/oauth2/token"
	entityName  = "allUsers"
	redirectURL = "urn:ietf:wg:oauth:2.0:oob"
)

var (
	cacheFile = flag.String("cache", "cache.json", "Token cache file")
	code      = flag.String("code", "", "Authorization Code")
	test      = flag.Bool("test", false, "Run locally in test mode without Raspistill")

	// For additional help with OAuth2 setup,
	// see http://goo.gl/cJ2OC and http://goo.gl/Y0os2

	// Raspistill commands
	raspistill_cmd  = "raspistill"
	raspistill_args = [...]string{"-o", "test.jpg", "-w", "640", "-h", "480"}
)

func fatalf(service *storage.Service, errorMessage string, args ...interface{}) {
	//	restoreOriginalState(service)
	log.Fatalf("Dying with error:\n"+errorMessage, args...)
}

func main() {
	flag.Parse()

	var appconfig AppConfig
	if _, err := toml.DecodeFile("config.toml", &appconfig); err != nil {
		log.Fatal("App configuration settings failed to load from file config.toml: ", err)
	}
	fmt.Println(appconfig.ClientId)
	fmt.Println(appconfig.ClientSecret)
	fmt.Println(appconfig.Oauth_code)

	// Set up a configuration boilerplate.
	config := &oauth.Config{
		ClientId:     appconfig.ClientId,
		ClientSecret: appconfig.ClientSecret,
		Scope:        scope,
		AuthURL:      authURL,
		TokenURL:     tokenURL,
		TokenCache:   oauth.CacheFile(*cacheFile),
		RedirectURL:  redirectURL,
	}

	// Set up a transport using the config
	transport := &oauth.Transport{
		Config:    config,
		Transport: http.DefaultTransport,
	}

	token, err := config.TokenCache.Token()
	if err != nil {

		if *code == "" {
			if appconfig.Oauth_code == "" {
				url := config.AuthCodeURL("")
				fmt.Println("Visit URL to get a code then run again with -code=YOUR_CODE")
				fmt.Println(url)
				os.Exit(1)
			} else {
				*code = appconfig.Oauth_code
			}
		}

		// Exchange auth code for access token
		token, err = transport.Exchange(*code)
		if err != nil {
			log.Fatal("Exchange: ", err)
		}
		fmt.Printf("Token is cached in %v\n", config.TokenCache)
	}
	transport.Token = token

	httpClient := transport.Client()
	service, err := storage.New(httpClient)

	// If the bucket already exists and the user has access, warn the user, but don't try to create it.
	if _, err := service.Buckets.Get(bucketName).Do(); err == nil {
		fmt.Printf("Bucket %s already exists - skipping buckets.insert call. \n", bucketName)
	} else {
		// Create a bucket.
		if res, err := service.Buckets.Insert(projectID, &storage.Bucket{Name: bucketName}).Do(); err == nil {
			fmt.Printf("Created bucket %v at location %v\n\n", res.Name, res.SelfLink)
		} else {
			fatalf(service, "Failed creating bucket %s: %v", bucketName, err)
		}
	}

	count := 0
	for {
		count++
		fmt.Printf("In update/check loop. Count: %d \n", count)

		time.Sleep(sleep_duration)
		// check server to see if someone has requested a new picture

		checkURL := url.URL{
			Path:   server_check_path + "/" + location_name,
			Host:   server_host,
			Scheme: "http",
		}
		resp, err := http.Get(checkURL.String())
		if err != nil {
			fmt.Printf("Failed to connect to server at %v.  Error: %v \n", checkURL.String(), err)
			continue
		}
		body, err := ioutil.ReadAll(resp.Body)
		fmt.Print("\n JSON received from clientcheck URL:\n")
		fmt.Print(string(body), "\n")

		var csm CheckStatusMessage
		err = json.Unmarshal(body, &csm)

		if err != nil {
			log.Fatal("JSON parameters from server in CheckStatusMessage could not be loaded:", err)
		}

		fmt.Print(csm)
		resp.Body.Close()

		if csm.NewPicRequested {

			fmt.Printf("Taking picture.  Count is: %v \n", count)
			// take a picture and store it to a local file
			if !*test {
				cmd := exec.Command(raspistill_cmd, raspistill_args[0], raspistill_args[1], raspistill_args[2],
					raspistill_args[3], raspistill_args[4], raspistill_args[5])
				err = cmd.Run()
				if err != nil {
					fatalf(service, "%v. Error capturing image with command: %v and args: %v \n", err, raspistill_cmd, raspistill_args)
				}
			}
			fmt.Printf("Inserting picture into bucket.\n")
			// Insert picture file into a bucket.
			// We name objects by adding the current time to the object path, after replacing spaces with _
			objectName := objectPath + strings.Replace(time.Now().String(), " ", "_", -1)
			object := &storage.Object{Name: objectName}
			file, err := os.Open(fileName)
			var LatestImageURL string
			if err != nil {
				fatalf(service, "Error opening %q: %v", fileName, err)
			}
			if res, err := service.Objects.Insert(bucketName, object).Media(file).Do(); err == nil {
				fmt.Printf("Created object with media at:\n %s\n", res.MediaLink)
				LatestImageURL = res.MediaLink
			} else {
				fatalf(service, "Objects.Insert failed: %v", err)
				LatestImageURL = "Error inserting image into Cloud Storage"
			}

			// This makes the picture object publicly accesible
			objectAcl := &storage.ObjectAccessControl{
				Bucket: bucketName, Entity: entityName, Object: objectName, Role: "READER",
			}
			if res, err := service.ObjectAccessControls.Insert(bucketName, objectName, objectAcl).Do(); err == nil {
				fmt.Printf("Result of inserting ACL for %v/%v:\n%v\n\n", bucketName, objectName, res)
			} else {
				fatalf(service, "Failed to insert ACL for %s/%s: %v.", bucketName, objectName, err)
			}

			// Inform the server about the location of the new picture that has been uploaded to the cloud
			updateURL := url.URL{
				Path:   server_update_path + "/" + location_name,
				Host:   server_host,
				Scheme: "http",
			}

			usm := UpdateServerMessage{
				LatestImageURL: LatestImageURL,
			}
			b, err := json.Marshal(usm)
			if err != nil {
				fmt.Printf("Failed to Marshal serve update data into JSON: %v", usm)
			}

			fmt.Printf("\nUpdating server at: %s \n", updateURL.String())

			buf := bytes.NewBuffer(b)
			fmt.Printf("JSON being sent to update link is: %v \n", buf)

			resp, err = http.Post(updateURL.String(), "application/json", buf)
			if err != nil {
				fmt.Printf("Failed to connect to server at %v.  Error: %v", updateURL.String(), err)
				continue
			}

			body, err := ioutil.ReadAll(resp.Body)
			fmt.Print("\n Response received from POST to update url:\n")
			fmt.Print(string(body), "\n")

		}

	}

}
