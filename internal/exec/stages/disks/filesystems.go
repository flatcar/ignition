// Copyright 2018 CoreOS, Inc.
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
	"errors"
	"fmt"
	iofs "io/fs"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"

	"github.com/flatcar-linux/ignition/internal/config/types"
	"github.com/flatcar-linux/ignition/internal/distro"
	"github.com/flatcar-linux/ignition/internal/exec/util"
)

var (
	ErrBadFilesystem = errors.New("filesystem is not of the correct type")
)

// createFilesystems creates the filesystems described in config.Storage.Filesystems.
func (s stage) createFilesystems(config types.Config) error {
	fss := make([]types.Mount, 0, len(config.Storage.Filesystems))
	for _, fs := range config.Storage.Filesystems {
		if fs.Mount != nil {
			fss = append(fss, *fs.Mount)
		}
	}

	if len(fss) == 0 {
		return nil
	}
	s.Logger.PushPrefix("createFilesystems")
	defer s.Logger.PopPrefix()

	devs := []string{}
	for _, fs := range fss {
		devs = append(devs, string(fs.Device))
	}

	if err := s.waitOnDevicesAndCreateAliases(devs, "filesystems"); err != nil {
		return err
	}

	// Create filesystems concurrently up to GOMAXPROCS
	concurrency := runtime.GOMAXPROCS(-1)
	work := make(chan types.Mount, len(fss))
	results := make(chan error)

	for i := 0; i < concurrency; i++ {
		go func() {
			for fs := range work {
				results <- s.createFilesystem(fs)
			}
		}()
	}

	for _, fs := range fss {
		work <- fs
	}
	close(work)

	// Return combined errors
	var errs []string
	for range fss {
		if err := <-results; err != nil {
			errs = append(errs, err.Error())
		}
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "\n"))
	}

	return nil
}

func (s stage) cleanFilesystemExcept(fs types.Mount) error {
	s.Logger.Info("filesystem at %q needs to be cleaned, preserving only %q", fs.Device, fs.CleanExcept)
	var cleanExceptRegex []*regexp.Regexp
	var keepButDontSkipRegex []*regexp.Regexp
	for _, regex := range fs.CleanExcept {
		// Remove final "/" in case it was passed to specify directories
		pathRegex := strings.TrimSuffix(string(regex), "/")
		if len(pathRegex) == 0 {
			// Keeping the whole top directory is a no-op
			return nil
		}
		regexKeep, err := regexp.Compile(pathRegex)
		if err != nil {
			return err
		}
		cleanExceptRegex = append(cleanExceptRegex, regexKeep)
		cleanExceptRegex = append(cleanExceptRegex, regexKeep)
		// Assemble a list of parent directory regular expressions,
		// "/" is not an allowed part of a regular expression and things
		// will break if it's not used as literal without repetitions/omissions
		// (probably an error is reported because the split string won't be valid)
		parts := strings.Split(pathRegex, "/")
		for i := len(parts) - 1; i > 1; i-- {
			partsParent := parts[0:i]
			regexForParent, err := regexp.Compile("/" + filepath.Join(partsParent...))
			if err != nil {
				return fmt.Errorf("split regex not valid, you must not use '/' as part of a regular expression:%v", err)
			}
			keepButDontSkipRegex = append(keepButDontSkipRegex, regexForParent)
		}
	}
	mnt, err := ioutil.TempDir("", "clean-filesystem-except")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %v", err)
	}
	// Make sure mnt does not end with a "/" because we use it to cut the path prefix
	mnt = strings.TrimSuffix(mnt, "/")
	defer os.Remove(mnt)
	dev := string(fs.Device)
	format := string(fs.Format)
	if err := syscall.Mount(dev, mnt, format, 0, ""); err != nil {
		return err
	}
	defer s.Logger.LogOp(
		func() error { return syscall.Unmount(mnt, 0) },
		"unmounting %q at %q", dev, mnt,
	)
	return filepath.WalkDir(mnt, func(path string, d iofs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("hit a deleted file (programming error): %v", err)
			}
			return err
		}
		// Assumption: The "path" we get is already absolute and for directories it does not end with a "/"
		matchPath := strings.Replace(path, mnt, "", 1)
		if matchPath == "" {
			// Skip top directory (first function call)
			return nil
		}
		match := false
		for _, regexKeep := range cleanExceptRegex {
			if loc := regexKeep.FindStringIndex(matchPath); loc != nil && loc[0] == 0 && loc[1] == len(matchPath) {
				match = true
				break
			}
		}
		matchkeepButDontSkip := false
		for _, regexKeepButDontSkip := range keepButDontSkipRegex {
			if loc := regexKeepButDontSkip.FindStringIndex(matchPath); loc != nil && loc[0] == 0 && loc[1] == len(matchPath) {
				matchkeepButDontSkip = true
				break
			}
		}
		if match && d.IsDir() {
			return iofs.SkipDir
		}
		if matchkeepButDontSkip && d.IsDir() {
			return nil
		}
		if match {
			// Keep matched file
			return nil
		}
		removeErr := os.RemoveAll(path)
		if removeErr != nil {
			return removeErr
		}
		if d.IsDir() {
			// We removed the directory and the contents already, and can't enter it anymore
			return iofs.SkipDir
		}
		return nil
	})
}

func (s stage) createFilesystem(fs types.Mount) error {
	info, err := s.readFilesystemInfo(fs)
	if err != nil {
		return err
	}

	if fs.Create != nil {
		// If we are using 2.0.0 semantics...

		if !fs.Create.Force && info.format != "" {
			s.Logger.Err("filesystem detected at %q (found %s) and force was not requested", fs.Device, info.format)
			return ErrBadFilesystem
		}
	} else if !fs.WipeFilesystem {
		// If the filesystem isn't forcefully being created, then we need
		// to check if it is of the correct type or that no filesystem exists.

		if (info.format == fs.Format || info.label == "OEM") &&
			(fs.Label == nil || info.label == *fs.Label) &&
			(fs.UUID == nil || canonicalizeFilesystemUUID(info.format, info.uuid) == canonicalizeFilesystemUUID(fs.Format, *fs.UUID)) {
			s.Logger.Info("filesystem at %q is already correctly formatted. Skipping mkfs...", fs.Device)
			if len(fs.CleanExcept) > 0 {
				return s.cleanFilesystemExcept(fs)
			}
			return nil
		} else if info.format != "" {
			s.Logger.Err("filesystem at %q is not of the correct type, label, or UUID (found %s, %q, %s) and a filesystem wipe was not requested", fs.Device, info.format, info.label, info.uuid)
			return ErrBadFilesystem
		}
	}

	mkfs := ""
	var args []string
	if fs.Create == nil {
		args = translateMountOptionSliceToStringSlice(fs.Options)
	} else {
		args = translateCreateOptionSliceToStringSlice(fs.Create.Options)
	}
	switch fs.Format {
	case "btrfs":
		mkfs = distro.BtrfsMkfsCmd()
		args = append(args, "--force")
		if fs.UUID != nil {
			args = append(args, "-U", canonicalizeFilesystemUUID(fs.Format, *fs.UUID))
		}
		if fs.Label != nil {
			args = append(args, "-L", *fs.Label)
		}
	case "ext4":
		mkfs = distro.Ext4MkfsCmd()
		args = append(args, "-F")
		if fs.UUID != nil {
			args = append(args, "-U", canonicalizeFilesystemUUID(fs.Format, *fs.UUID))
		}
		if fs.Label != nil {
			args = append(args, "-L", *fs.Label)
		}
	case "xfs":
		mkfs = distro.XfsMkfsCmd()
		args = append(args, "-f")
		if fs.UUID != nil {
			args = append(args, "-m", "uuid="+canonicalizeFilesystemUUID(fs.Format, *fs.UUID))
		}
		if fs.Label != nil {
			args = append(args, "-L", *fs.Label)
		}
	case "swap":
		mkfs = distro.SwapMkfsCmd()
		args = append(args, "-f")
		if fs.UUID != nil {
			args = append(args, "-U", canonicalizeFilesystemUUID(fs.Format, *fs.UUID))
		}
		if fs.Label != nil {
			args = append(args, "-L", *fs.Label)
		}
	case "vfat":
		mkfs = distro.VfatMkfsCmd()
		// There is no force flag for mkfs.vfat, it always destroys any data on
		// the device at which it is pointed.
		if fs.UUID != nil {
			args = append(args, "-i", canonicalizeFilesystemUUID(fs.Format, *fs.UUID))
		}
		if fs.Label != nil {
			args = append(args, "-n", *fs.Label)
		}
	default:
		return fmt.Errorf("unsupported filesystem format: %q", fs.Format)
	}

	devAlias := util.DeviceAlias(string(fs.Device))
	args = append(args, devAlias)
	if _, err := s.Logger.LogCmd(
		exec.Command(mkfs, args...),
		"creating %q filesystem on %q",
		fs.Format, devAlias,
	); err != nil {
		return fmt.Errorf("mkfs failed: %v", err)
	}

	return nil
}

// golang--
func translateMountOptionSliceToStringSlice(opts []types.MountOption) []string {
	newOpts := make([]string, len(opts))
	for i, o := range opts {
		newOpts[i] = string(o)
	}
	return newOpts
}

// golang--
func translateCreateOptionSliceToStringSlice(opts []types.CreateOption) []string {
	newOpts := make([]string, len(opts))
	for i, o := range opts {
		newOpts[i] = string(o)
	}
	return newOpts
}

type filesystemInfo struct {
	format string
	uuid   string
	label  string
}

func (s stage) readFilesystemInfo(fs types.Mount) (filesystemInfo, error) {
	res := filesystemInfo{}
	err := s.Logger.LogOp(
		func() error {
			var err error
			res.format, err = util.FilesystemType(fs.Device)
			if err != nil {
				return err
			}
			res.uuid, err = util.FilesystemUUID(fs.Device)
			if err != nil {
				return err
			}
			res.label, err = util.FilesystemLabel(fs.Device)
			if err != nil {
				return err
			}
			s.Logger.Info("found %s filesystem at %q with uuid %q and label %q", res.format, fs.Device, res.uuid, res.label)
			return nil
		},
		"determining filesystem type of %q", fs.Device,
	)

	return res, err
}

// canonicalizeFilesystemUUID does the minimum amount of canonicalization
// required to make two valid equivalent UUIDs compare equal, but doesn't
// attempt to fully validate the UUID.
func canonicalizeFilesystemUUID(format, uuid string) string {
	uuid = strings.ToLower(uuid)
	if format == "vfat" {
		// FAT uses a 32-bit volume ID instead of a UUID. blkid
		// (and the rest of the world) formats it as A1B2-C3D4, but
		// mkfs.fat doesn't permit the dash, so strip it. Older
		// versions of Ignition would fail if the config included
		// the dash, so we need to support omitting it.
		if len(uuid) >= 5 && uuid[4] == '-' {
			uuid = uuid[0:4] + uuid[5:]
		}
	}
	return uuid
}
