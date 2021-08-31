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

package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/coreos/ignition/v2/internal/exec"
	"github.com/coreos/ignition/v2/internal/exec/stages"
	_ "github.com/coreos/ignition/v2/internal/exec/stages/disks"
	_ "github.com/coreos/ignition/v2/internal/exec/stages/fetch"
	_ "github.com/coreos/ignition/v2/internal/exec/stages/fetch_offline"
	_ "github.com/coreos/ignition/v2/internal/exec/stages/files"
	_ "github.com/coreos/ignition/v2/internal/exec/stages/kargs"
	_ "github.com/coreos/ignition/v2/internal/exec/stages/mount"
	_ "github.com/coreos/ignition/v2/internal/exec/stages/umount"
	"github.com/coreos/ignition/v2/internal/log"
	"github.com/coreos/ignition/v2/internal/platform"
	"github.com/coreos/ignition/v2/internal/state"
	"github.com/coreos/ignition/v2/internal/version"
)

func main() {
	flags := struct {
		configCache  string
		fetchTimeout time.Duration
		needNet      string
		platform     platform.Name
		oem          platform.Name
		root         string
		stage        stages.Name
		stateFile    string
		version      bool
		logToStdout  bool
	}{}

	flag.StringVar(&flags.configCache, "config-cache", "/run/ignition.json", "where to cache the config")
	flag.DurationVar(&flags.fetchTimeout, "fetch-timeout", exec.DefaultFetchTimeout, "initial duration for which to wait for config")
	flag.StringVar(&flags.needNet, "neednet", "/run/ignition/neednet", "flag file to write from fetch-offline if networking is needed")
	flag.Var(&flags.platform, "platform", fmt.Sprintf("current platform. %v", platform.Names()))
	flag.Var(&flags.oem, "oem", fmt.Sprintf("current oem. %v", platform.Names()))
	flag.StringVar(&flags.root, "root", "/", "root of the filesystem")
	flag.Var(&flags.stage, "stage", fmt.Sprintf("execution stage. %v", stages.Names()))
	flag.StringVar(&flags.stateFile, "state-file", "/run/ignition/state", "where to store internal state")
	flag.BoolVar(&flags.version, "version", false, "print the version and exit")
	flag.BoolVar(&flags.logToStdout, "log-to-stdout", false, "log to stdout instead of the system log when set")

	flag.Parse()

	if flags.version {
		fmt.Printf("%s\n", version.String)
		return
	}

	// keep compatiblity with --oem
	if flags.platform == "" && flags.oem == "" {
		fmt.Fprint(os.Stderr, "'--platform' or '--oem' must be provided\n")
		os.Exit(2)
	}

	if flags.stage == "" {
		fmt.Fprint(os.Stderr, "'--stage' must be provided\n")
		os.Exit(2)
	}

	// if --oem is set it means --platform is not known, we can
	// safely override it then
	if flags.oem != "" {
		flags.platform = flags.oem
	}

	logger := log.New(flags.logToStdout)
	defer logger.Close()

	logger.Info(version.String)
	logger.Info("Stage: %v", flags.stage)

	platformConfig := platform.MustGet(flags.platform.String())
	fetcher, err := platformConfig.NewFetcherFunc()(&logger)
	if err != nil {
		logger.Crit("failed to generate fetcher: %s", err)
		os.Exit(3)
	}
	state, err := state.Load(flags.stateFile)
	if err != nil {
		logger.Crit("reading state: %s", err)
		os.Exit(3)
	}
	engine := exec.Engine{
		Root:           flags.root,
		FetchTimeout:   flags.fetchTimeout,
		Logger:         &logger,
		NeedNet:        flags.needNet,
		ConfigCache:    flags.configCache,
		PlatformConfig: platformConfig,
		Fetcher:        &fetcher,
		State:          &state,
	}

	err = engine.Run(flags.stage.String())
	if statusErr := engine.PlatformConfig.Status(flags.stage.String(), *engine.Fetcher, err); statusErr != nil {
		logger.Err("POST Status error: %v", statusErr.Error())
	}
	if err != nil {
		logger.Crit("Ignition failed: %v", err.Error())
		os.Exit(1)
	}
	if err := engine.State.Save(flags.stateFile); err != nil {
		logger.Crit("writing state: %v", err)
		os.Exit(1)
	}
	logger.Info("Ignition finished successfully")
}
