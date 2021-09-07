// Copyright 2020 Red Hat, Inc.
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

package v3_4_experimental

import (
	"encoding/json"
	"fmt"

	"github.com/coreos/ignition/config/v2_4"
	"github.com/coreos/ignition/v2/config/merge"
	"github.com/coreos/ignition/v2/config/shared/errors"
	"github.com/coreos/ignition/v2/config/util"
	prev "github.com/coreos/ignition/v2/config/v3_3"
	"github.com/coreos/ignition/v2/config/v3_4_experimental/translate"
	"github.com/coreos/ignition/v2/config/v3_4_experimental/types"
	"github.com/coreos/ignition/v2/config/validate"

	"github.com/coreos/go-semver/semver"
	"github.com/coreos/vcontext/report"

	"github.com/coreos/ign-converter/translate/v24tov31"
)

func Merge(parent, child types.Config) types.Config {
	res, _ := merge.MergeStructTranscribe(parent, child)
	return res.(types.Config)
}

// Parse parses the raw config into a types.Config struct and generates a report of any
// errors, warnings, info, and deprecations it encountered
func Parse(rawConfig []byte) (types.Config, report.Report, error) {
	if len(rawConfig) == 0 {
		return types.Config{}, report.Report{}, errors.ErrEmpty
	}

	var config types.Config
	if rpt, err := util.HandleParseErrors(rawConfig, &config); err != nil {
		return types.Config{}, rpt, err
	}

	version, err := semver.NewVersion(config.Ignition.Version)

	if err != nil || *version != types.MaxVersion {
		return types.Config{}, report.Report{}, errors.ErrUnknownVersion
	}

	rpt := validate.ValidateWithContext(config, rawConfig)
	if rpt.IsFatal() {
		return types.Config{}, rpt, errors.ErrInvalid
	}

	return config, rpt, nil
}

// ParseCompatibleVersion parses the raw config of version 3.4.0-experimental or
// lesser into a 3.4-exp types.Config struct and generates a report of any errors,
// warnings, info, and deprecations it encountered
func ParseCompatibleVersion(raw []byte) (types.Config, report.Report, error) {
	version, rpt, err := util.GetConfigVersion(raw)
	if err != nil {
		return types.Config{}, rpt, err
	}

	// if the version is 2.x or 1.x, we
	// convert it to 3.1
	if version.Major != 3 {
		// Parse should fallback on every 2.x supported version
		cfg, _, err := v2_4.Parse(raw)
		if err != nil || rpt.IsFatal() {
			return types.Config{}, report.Report{}, fmt.Errorf("unable to parse 2.x ignition: %w", err)
		}

		/*
			map[string]string{} is required by the ign-converter
			Ignition Spec 3 will mount filesystems at the mountpoint specified by path when running.
			Filesystems no longer have the name field and files, links, and directories no longer specify the filesystem by name.
			This means to translate filesystems (with the exception of root),
			you must also provide a mapping of filesystem name to absolute path, e.g.
			```
			map[string]string{"var": "/var"}
			```
		*/
		newCfg, err := v24tov31.Translate(cfg, map[string]string{})
		if err != nil {
			return types.Config{}, report.Report{}, fmt.Errorf("unable to translate 2.x ignition to 3.1: %w", err)

		}

		// update raw in place to continue with the 3.x logic
		raw, err = json.Marshal(newCfg)
		if err != nil {
			return types.Config{}, report.Report{}, fmt.Errorf("unable to render JSON: %w", err)
		}
	}

	if version == types.MaxVersion {
		return Parse(raw)
	}
	prevCfg, r, err := prev.ParseCompatibleVersion(raw)
	if err != nil {
		return types.Config{}, r, err
	}
	return translate.Translate(prevCfg), r, nil
}
