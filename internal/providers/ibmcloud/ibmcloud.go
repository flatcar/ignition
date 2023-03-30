// Copyright 2019 Red Hat, Inc.
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

// The IBM cloud provider fetches configurations from the userdata available in
// the config-drive.
// NOTE: This provider is still EXPERIMENTAL.

package ibmcloud

import (
	"context"
	"path/filepath"
	"time"

	"github.com/coreos/ignition/v2/config/v3_1_experimental/types"
	"github.com/coreos/ignition/v2/internal/distro"
	"github.com/coreos/ignition/v2/internal/providers/openstack"
	"github.com/coreos/ignition/v2/internal/providers/util"
	"github.com/coreos/ignition/v2/internal/resource"

	"github.com/coreos/vcontext/report"
)

const (
	cidataPath  = "/user-data"
	deviceLabel = "cidata"
)

func FetchConfig(f *resource.Fetcher) (types.Config, report.Report, error) {
	var data []byte
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

	dispatch := func(name string, fn func() ([]byte, error)) {
		raw, err := fn()
		if err != nil {
			switch err {
			case context.Canceled:
			case context.DeadlineExceeded:
				f.Logger.Err("timed out while fetching config from %s", name)
			default:
				f.Logger.Err("failed to fetch config from %s: %v", name, err)
			}
			return
		}

		data = raw
		cancel()
	}

	go dispatch("config drive (cidata)", func() ([]byte, error) {
		return openstack.FetchConfigFromDevice(f.Logger, ctx, filepath.Join(distro.DiskByLabelDir(), deviceLabel))
	})

	<-ctx.Done()
	if ctx.Err() == context.DeadlineExceeded {
		f.Logger.Info("cidata drive was not available in time. Continuing without a config...")
	}

	return util.ParseConfig(f.Logger, data)
}
