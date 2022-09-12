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

// The storage stage is responsible for partitioning disks, creating RAID
// arrays, formatting partitions, writing files, writing systemd units, and
// writing network units.

package disks

import (
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/flatcar/ignition/internal/config/types"
	"github.com/flatcar/ignition/internal/distro"
	"github.com/flatcar/ignition/internal/exec/stages"
	"github.com/flatcar/ignition/internal/exec/util"
	"github.com/flatcar/ignition/internal/log"
	"github.com/flatcar/ignition/internal/resource"
	"github.com/flatcar/ignition/internal/systemd"
)

const (
	name = "disks"
)

func init() {
	stages.Register(creator{})
}

type creator struct{}

func (creator) Create(logger *log.Logger, root string, f resource.Fetcher) stages.Stage {
	return &stage{
		Util: util.Util{
			DestDir: root,
			Logger:  logger,
			Fetcher: f,
		},
	}
}

func (creator) Name() string {
	return name
}

type stage struct {
	util.Util

	client *resource.HttpClient
}

func (stage) Name() string {
	return name
}

func (s stage) Run(config types.Config) error {
	// Interacting with disks/partitions/raids/filesystems in general can cause
	// udev races. If we do not need to  do anything, we also do not need to
	// do the udevadm settle and can just return here. There is always an implicit
	// root filesystem defined in the base config, so the lowest number of
	// filesystems is 1.
	if len(config.Storage.Disks) == 0 &&
		len(config.Storage.Raid) == 0 &&
		len(config.Storage.Filesystems) == 1 {
		return nil
	}

	if err := s.createPartitions(config); err != nil {
		return fmt.Errorf("create partitions failed: %v", err)
	}

	if err := s.createRaids(config); err != nil {
		return fmt.Errorf("failed to create raids: %v", err)
	}

	if err := s.createFilesystems(config); err != nil {
		return fmt.Errorf("failed to create filesystems: %v", err)
	}

	return nil
}

// waitForUdev triggers a tagged event and waits for it to bubble up
// again. This ensures that udev processed the device changes.
// The requirement is that the used device path exists and itself is
// not recreated by udev seeing the changes done. Thus, resolve a
// /dev/disk/by-something/X symlink before performing the device
// changes (i.e., pass /run/ignition/dev_aliases/X) and, e.g., don't
// call it for a partition but the full disk if you modified the
// partition table.
func (s stage) waitForUdev(dev, ctxt string) error {
	// Resolve the original /dev/ABC entry because udevadm wants
	// this as argument instead of a symlink like
	// /run/ignition/dev_aliases/X.
	devPath, err := filepath.EvalSymlinks(dev)
	if err != nil {
		return fmt.Errorf("failed to resolve device alias %q on %s: %v", dev, ctxt, err)
	}
	// By triggering our own event and waiting for it we know that udev
	// will have processed the device changes, a bare "udevadm settle"
	// is prone to races with the inotify queue. We expect the /dev/DISK
	// entry to exist because this function is either called for the full
	// disk and only the /dev/DISKpX partition entries will change, or the
	// function is called for a partition where the contents changed and
	// nothing causes the kernel/udev to reread the partition table and
	// recreate the /dev/DISKpX entries. If that was the case best we could
	// do here is to add a retry loop (and relax the function comment).
	_, err = s.Logger.LogCmd(
		exec.Command(distro.UdevadmCmd(), "trigger", "--settle",
			devPath), "waiting for triggered uevent")
	if err != nil {
		return fmt.Errorf("udevadm trigger failed on %s: %v", ctxt, err)
	}

	return nil
}

// waitOnDevices waits for the devices enumerated in devs as a logged operation
// using ctxt for the logging and systemd unit identity.
func (s stage) waitOnDevices(devs []string, ctxt string) error {
	if err := s.LogOp(
		func() error { return systemd.WaitOnDevices(devs, ctxt) },
		"waiting for devices %v", devs,
	); err != nil {
		return fmt.Errorf("failed to wait on %s devs: %v", ctxt, err)
	}

	return nil
}

// createDeviceAliases creates device aliases for every device in devs.
func (s stage) createDeviceAliases(devs []string) error {
	for _, dev := range devs {
		target, err := util.CreateDeviceAlias(dev)
		if err != nil {
			return fmt.Errorf("failed to create device alias for %q: %v", dev, err)
		}
		s.Logger.Info("created device alias for %q: %q -> %q", dev, util.DeviceAlias(dev), target)
	}

	return nil
}

// waitOnDevicesAndCreateAliases simply wraps waitOnDevices and createDeviceAliases.
func (s stage) waitOnDevicesAndCreateAliases(devs []string, ctxt string) error {
	if err := s.waitOnDevices(devs, ctxt); err != nil {
		return err
	}

	if err := s.createDeviceAliases(devs); err != nil {
		return err
	}

	return nil
}
