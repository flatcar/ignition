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

// The vmware provider fetches a configuration from the VMware Guest Info
// interface.

package vmware

import (
	"net/url"

	"github.com/coreos/ignition/v2/config/v3_4_experimental/types"
	"github.com/coreos/ignition/v2/internal/providers"
	"github.com/coreos/ignition/v2/internal/providers/util"
	"github.com/coreos/ignition/v2/internal/resource"

	"github.com/coreos/vcontext/report"
	"github.com/vmware/vmw-guestinfo/rpcvmx"
	"github.com/vmware/vmw-guestinfo/vmcheck"
	"github.com/vmware/vmw-ovflib"
)

func FetchConfig(f *resource.Fetcher) (types.Config, report.Report, error) {
	if isVM, err := vmcheck.IsVirtualWorld(); err != nil {
		return types.Config{}, report.Report{}, err
	} else if !isVM {
		return types.Config{}, report.Report{}, providers.ErrNoProvider
	}

	config, err := fetchDataConfig(f)
	if err == nil && len(config) == 0 {
		config, err = fetchUrlConfig(f)
	}
	if err != nil {
		return types.Config{}, report.Report{}, err
	}

	f.Logger.Debug("config successfully fetched")
	return util.ParseConfig(f.Logger, config)
}

func fetchDataConfig(f *resource.Fetcher) ([]byte, error) {
	var data string
	var encoding string
	var err error

	data, err = getVariable(f, "ignition.config.data")
	if err == nil && data != "" {
		encoding, err = getVariable(f, "ignition.config.data.encoding")
	} else {
		data, err = getVariable(f, "coreos.config.data")
		if err == nil && data != "" {
			encoding, err = getVariable(f, "coreos.config.data.encoding")
		}
	}
	// Do not check against err from "encoding" because leaving it empty is ok
	if data == "" {
		f.Logger.Debug("failed to fetch config")
		return []byte{}, nil
	}

	decodedData, err := decodeConfig(config{
		data:     data,
		encoding: encoding,
	})
	if err != nil {
		f.Logger.Debug("failed to decode config: %v", err)
		return nil, err
	}

	return decodedData, nil
}

func fetchUrlConfig(f *resource.Fetcher) ([]byte, error) {
	rawUrl, err := getVariable(f, "ignition.config.url")
	if err != nil || rawUrl == "" {
		rawUrl, err = getVariable(f, "coreos.config.url")
	}
	if err != nil || rawUrl == "" {
		f.Logger.Info("no config URL provided")
		return []byte{}, nil
	}

	f.Logger.Debug("found url: %q", rawUrl)

	url, err := url.Parse(rawUrl)
	if err != nil {
		f.Logger.Err("failed to parse url: %v", err)
		return nil, err
	}
	if url == nil {
		return []byte{}, nil
	}

	data, err := f.FetchToBuffer(*url, resource.FetchOptions{})
	if err != nil {
		return nil, err
	}

	return data, nil
}

func getVariable(f *resource.Fetcher, key string) (string, error) {
	info := rpcvmx.NewConfig()

	var ovfData string

	ovfEnv, err := info.String("ovfenv", "")
	if err != nil {
		f.Logger.Warning("failed to fetch ovfenv: %v. Continuing...", err)
	} else if ovfEnv != "" {
		f.Logger.Debug("using OVF environment from guestinfo")
		env, err := ovf.ReadEnvironment([]byte(ovfEnv))
		if err != nil {
			f.Logger.Warning("failed to parse OVF environment: %v. Continuing...", err)
		} else {
			ovfData = env.Properties["guestinfo."+key]
		}
	}

	// The guest variables get preference over the ovfenv variables which are given here as fallback
	data, err := info.String(key, ovfData)
	if err != nil {
		f.Logger.Debug("failed to fetch variable, falling back to ovfenv value: %v", err)
		return ovfData, nil
	}

	// An empty string will be returned if nothing was found
	return data, nil
}
