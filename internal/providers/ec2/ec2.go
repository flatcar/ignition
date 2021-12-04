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
	"github.com/flatcar-linux/ignition/config/validate/report"
	"github.com/flatcar-linux/ignition/internal/config/types"
	"github.com/flatcar-linux/ignition/internal/log"
	"github.com/flatcar-linux/ignition/internal/providers/util"
	"github.com/flatcar-linux/ignition/internal/resource"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials/ec2rolecreds"
	"github.com/aws/aws-sdk-go/aws/ec2metadata"
	"github.com/aws/aws-sdk-go/aws/session"
)

func FetchConfig(f *resource.Fetcher) (types.Config, report.Report, error) {
	data, err := ec2metadata.New(f.AWSSession).GetUserData()
	if err != nil && err != resource.ErrNotFound {
		return types.Config{}, report.Report{}, err
	}

	// Determine the partition and region this instance is in
	regionHint, err := ec2metadata.New(f.AWSSession).Region()
	if err != nil {
		regionHint = "us-east-1"
	}
	f.S3RegionHint = regionHint

	return util.ParseConfig(f.Logger, []byte(data))
}

func NewFetcher(l *log.Logger) (resource.Fetcher, error) {
	sess, err := session.NewSession(aws.NewConfig())
	if err != nil {
		return resource.Fetcher{}, err
	}
	sess.Config.Credentials = ec2rolecreds.NewCredentials(sess)

	return resource.Fetcher{
		Logger:     l,
		AWSSession: sess,
	}, nil
}
