// Copyright 2017 CoreOS, Inc.
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

package filesystems

import (
	"github.com/flatcar-linux/ignition/v2/tests/register"
	"github.com/flatcar-linux/ignition/v2/tests/types"
)

func init() {
	register.Register(register.NegativeTest, NoDevice())
}

func NoDevice() types.Test {
	name := "filesystems.nodevice"
	in := types.GetBaseDisk()
	out := in
	config := `{
		"ignition": {"version": "$version"},
		"storage": {
			"filesystems": [{
				"format": "ext4",
				"path": "/tmp0",
			}]
		}
	}`
	configMinVersion := "3.0.0"

	return types.Test{
		Name:              name,
		In:                in,
		Out:               out,
		Config:            config,
		ConfigShouldBeBad: true,
		ConfigMinVersion:  configMinVersion,
	}
}
