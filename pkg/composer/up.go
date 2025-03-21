/*
   Copyright The containerd Authors.

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

package composer

import (
	"context"
	"fmt"
	"os"

	"github.com/compose-spec/compose-go/types"
	"github.com/containerd/nerdctl/pkg/composer/serviceparser"
	"github.com/containerd/nerdctl/pkg/reflectutil"

	"github.com/sirupsen/logrus"
)

type UpOptions struct {
	Detach        bool
	NoBuild       bool
	NoColor       bool
	NoLogPrefix   bool
	ForceBuild    bool
	IPFS          bool
	QuietPull     bool
	RemoveOrphans bool
	Scale         map[string]uint64 // map of service name to replicas
}

func (c *Composer) Up(ctx context.Context, uo UpOptions, services []string) error {
	for shortName := range c.project.Networks {
		if err := c.upNetwork(ctx, shortName); err != nil {
			return err
		}
	}

	for shortName := range c.project.Volumes {
		if err := c.upVolume(ctx, shortName); err != nil {
			return err
		}
	}

	for shortName, secret := range c.project.Secrets {
		obj := types.FileObjectConfig(secret)
		if err := validateFileObjectConfig(obj, shortName, "service", c.project); err != nil {
			return err
		}
	}

	for shortName, config := range c.project.Configs {
		obj := types.FileObjectConfig(config)
		if err := validateFileObjectConfig(obj, shortName, "config", c.project); err != nil {
			return err
		}
	}

	var parsedServices []*serviceparser.Service
	// use WithServices to sort the services in dependency order
	if err := c.project.WithServices(services, func(svc types.ServiceConfig) error {
		replicas, ok := uo.Scale[svc.Name]
		if ok {
			if svc.Deploy == nil {
				svc.Deploy = &types.DeployConfig{}
			}
			svc.Deploy.Replicas = &replicas
		}
		ps, err := serviceparser.Parse(c.project, svc)
		if err != nil {
			return err
		}
		parsedServices = append(parsedServices, ps)
		return nil
	}); err != nil {
		return err
	}

	if err := c.upServices(ctx, parsedServices, uo); err != nil {
		return err
	}

	if uo.RemoveOrphans {
		orphans, err := c.getOrphanContainers(ctx, parsedServices)
		if err != nil {
			return fmt.Errorf("error getting orphaned containers: %s", err)
		}
		if len(orphans) == 0 {
			return nil
		}
		if err := c.downContainers(ctx, orphans, true); err != nil {
			return fmt.Errorf("error removing orphaned containers: %s", err)
		}
	}

	return nil
}

func validateFileObjectConfig(obj types.FileObjectConfig, shortName, objType string, project *types.Project) error {
	if unknown := reflectutil.UnknownNonEmptyFields(&obj, "Name", "External", "File"); len(unknown) > 0 {
		logrus.Warnf("Ignoring: %s %s: %+v", objType, shortName, unknown)
	}
	if obj.External.External || obj.External.Name != "" {
		return fmt.Errorf("%s %q: external object is not supported", objType, shortName)
	}
	if obj.File == "" {
		return fmt.Errorf("%s %q: lacks file path", objType, shortName)
	}
	fullPath := project.RelativePath(obj.File)
	if _, err := os.Stat(fullPath); err != nil {
		return fmt.Errorf("%s %q: failed to open file %q: %w", objType, shortName, fullPath, err)
	}
	return nil
}
