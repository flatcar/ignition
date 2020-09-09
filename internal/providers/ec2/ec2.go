// Copyright 2015 CoreOS, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// The ec2 provider fetches a remote configuration from the ec2 user-data
// metadata service URL.

package ec2

import (
	"bytes"
	"errors"
	"io"
	"io/ioutil"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"

	"github.com/coreos/ignition/config/validate/report"
	"github.com/coreos/ignition/internal/config"
	"github.com/coreos/ignition/internal/config/types"
	"github.com/coreos/ignition/internal/log"
	"github.com/coreos/ignition/internal/providers/util"
	"github.com/coreos/ignition/internal/resource"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials/ec2rolecreds"
	"github.com/aws/aws-sdk-go/aws/ec2metadata"
	"github.com/aws/aws-sdk-go/aws/session"
)

var (
	userdataUrl = url.URL{
		Scheme: "http",
		Host:   "169.254.169.254",
		Path:   "2009-04-04/user-data",
	}
	ErrNoBoundary      = errors.New("found multipart message but no boundary; could not parse")
	ErrMultipleConfigs = errors.New("found multiple configs in multipart response")
)

func FetchConfig(f *resource.Fetcher) (types.Config, report.Report, error) {
	var ResponseHeaders http.Header

	data, err := f.FetchToBuffer(userdataUrl, resource.FetchOptions{
		Headers:         resource.ConfigHeaders,
		ResponseHeaders: &ResponseHeaders,
	})
	if err != nil && err != resource.ErrNotFound {
		return types.Config{}, report.Report{}, err
	}

	// cluster API for AWS returns the Ignition config as a section of a multipart sequence.
	// Detect if we got a multipart message, ensure that there is only one Ignition config,
	// and extract it.
	mediaType, params, err := mime.ParseMediaType(ResponseHeaders.Get("Content-Type"))
	if err != nil || mediaType != "multipart/mixed" {
		// either unset or not multipart/mixed, just return the blob
		// we don't require proper Content-Type headers
		return config.Parse(data)
	}
	boundary, ok := params["boundary"]
	if !ok {
		return types.Config{}, report.Report{}, ErrNoBoundary
	}
	mpReader := multipart.NewReader(bytes.NewReader(data), boundary)
	var ignConfig []byte
	for {
		part, err := mpReader.NextPart()
		if err == io.EOF {
			break
		} else if err != nil {
			return types.Config{}, report.Report{}, err
		}
		partType := part.Header.Get("Content-Type")
		if strings.HasPrefix(partType, "application/vnd.coreos.ignition+json") {
			if ignConfig != nil {
				// found more than one ignition config, die.
				return types.Config{}, report.Report{}, ErrMultipleConfigs
			}
			ignConfig, err = ioutil.ReadAll(part)
			if err != nil {
				return types.Config{}, report.Report{}, err
			}
		}
	}
	data = ignConfig

	// Determine the partition and region this instance is in
	regionHint, err := ec2metadata.New(f.AWSSession).Region()
	if err != nil {
		regionHint = "us-east-1"
	}
	f.S3RegionHint = regionHint

	return util.ParseConfig(f.Logger, data)
}

func NewFetcher(l *log.Logger) (resource.Fetcher, error) {
	sess, err := session.NewSession(&aws.Config{})
	if err != nil {
		return resource.Fetcher{}, err
	}
	sess.Config.Credentials = ec2rolecreds.NewCredentials(sess)

	return resource.Fetcher{
		Logger:     l,
		AWSSession: sess,
	}, nil
}
