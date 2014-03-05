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
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"code.google.com/p/goauth2/oauth"
	"code.google.com/p/google-api-go-client/storage/v1beta2"
)

const (
	// Change these variable to match your personal information.
	bucketName         = "pipark2014"
	projectID          = "pipark2014"
	server_host        = projectID + ".appspot.com"
	server_check_path  = "/clientcheck"
	server_update_path = "/clientupdate"
	location_name      = "300ThirdStreet"

	clientId     = "27416394365-napaad4ep26p0ol8dds9od47ml404rpo.apps.googleusercontent.com"
	clientSecret = "iSiusb81xfteMv5Un3hJGzHs"
	oauth_code   = "4/nKY1pPvVc07TQyt_yxogZXym5B6R.ImIrNr7AvHkcEnp6UAPFm0HAS7YSiQI"

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

	// Set up a configuration boilerplate.
	config = &oauth.Config{
		ClientId:     clientId,
		ClientSecret: clientSecret,
		Scope:        scope,
		AuthURL:      authURL,
		TokenURL:     tokenURL,
		TokenCache:   oauth.CacheFile(*cacheFile),
		RedirectURL:  redirectURL,
	}

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

	// Set up a transport using the config
	transport := &oauth.Transport{
		Config:    config,
		Transport: http.DefaultTransport,
	}

	token, err := config.TokenCache.Token()
	if err != nil {

		if *code == "" {
			if oauth_code == "" {
				url := config.AuthCodeURL("")
				fmt.Println("Visit URL to get a code then run again with -code=YOUR_CODE")
				fmt.Println(url)
				os.Exit(1)
			} else {
				*code = oauth_code
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
		fmt.Printf("Bucket %s already exists - skipping buckets.insert call.", bucketName)
	} else {
		// Create a bucket.
		if res, err := service.Buckets.Insert(projectID, &storage.Bucket{Name: bucketName}).Do(); err == nil {
			fmt.Printf("Created bucket %v at location %v\n\n", res.Name, res.SelfLink)
		} else {
			fatalf(service, "Failed creating bucket %s: %v", bucketName, err)
		}
	}

	for {

		time.Sleep(sleep_duration)

		// check server to see if someone has requested a new picture

		checkURL := url.URL{
			Path: server_check_path + "/" + location_name,
			Host: server_host,
		}
		resp, err := http.Get(checkURL.String())
		if err != nil {
			fmt.Printf("Failed to contact server at %v.  Error: %v", checkURL.String(), err)
			continue
		}
		body, err := ioutil.ReadAll(resp.Body)
		resp.Body.Close()

		// take a picture and store it to a local file
		if !*test {
			cmd := exec.Command(raspistill_cmd, raspistill_args[0], raspistill_args[1], raspistill_args[2],
				raspistill_args[3], raspistill_args[4], raspistill_args[5])
			err = cmd.Run()
			if err != nil {
				fatalf(service, "%v. Error capturing image with command: %v and args: %v", err, raspistill_cmd, raspistill_args)
			}
		}

		// Insert picture file into a bucket.
		// We name objects by adding the current time to the object path, after replacing spaces with _
		objectName := objectPath + strings.Replace(time.Now().String(), " ", "_", -1)
		object := &storage.Object{Name: objectName}
		file, err := os.Open(fileName)
		if err != nil {
			fatalf(service, "Error opening %q: %v", fileName, err)
		}
		if res, err := service.Objects.Insert(bucketName, object).Media(file).Do(); err == nil {
			fmt.Printf("Created object %v at location %v with media at %v\n\n", res.Name, res.SelfLink, res.MediaLink)
		} else {
			fatalf(service, "Objects.Insert failed: %v", err)
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

	}

}
