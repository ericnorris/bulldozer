package statemachine

import (
	"context"
	"log"
	"time"

	"github.com/pkg/errors"
	"google.golang.org/api/compute/v1"
)

const (
	tickInterval = 60 * time.Second
	maxTicks     = 60
)

type Runner struct {
	computeAPI computeAPI
	sleepFunc  func(duration time.Duration)

	projectID    string
	location     RegionOrZone
	migName      string
	templateName string
}

func New(ctx context.Context, projectID string, location RegionOrZone, migName, templateName string) (*Runner, error) {
	computeAPI, err := newGoogleComputeAPI(ctx)

	if err != nil {
		return nil, err
	}

	return &Runner{
		computeAPI: computeAPI,
		sleepFunc:  time.Sleep,

		projectID:    projectID,
		location:     location,
		migName:      migName,
		templateName: templateName,
	}, nil
}

type clusterInfo struct {
	group    *compute.InstanceGroupManager
	template *compute.InstanceTemplate
	backend  *compute.BackendService
}

func (r *Runner) Start(ctx context.Context) error {
	log.Printf("starting rollout of template '%s' to managed instance group '%s'", r.templateName, r.migName)

	clusterInfo, err := r.getInfo(ctx)

	if err != nil {
		return err
	}

	loopIterations := 0

	for {
		log.Printf("beginning rollout loop iteration #%d", loopIterations)

		if err := r.scale(ctx, clusterInfo); err != nil {
			return err
		}

		clusterInfo, err = r.waitUntilStable(ctx, clusterInfo)

		if err != nil {
			return err
		}

		if err := r.checkBackendServiceHealth(ctx, clusterInfo); err != nil {
			return err
		}

		if r.isDone(clusterInfo) {
			break
		}

		loopIterations++
	}

	log.Printf("rollout complete")

	return nil
}

func (r *Runner) getInfo(ctx context.Context) (clusterInfo, error) {
	var emptyInfo clusterInfo

	group, err := r.computeAPI.GetMIG(ctx, r.projectID, r.location, r.migName)

	if err != nil {
		return emptyInfo, err
	}

	template, err := r.computeAPI.GetInstanceTemplate(ctx, r.projectID, r.templateName)

	if err != nil {
		return emptyInfo, err
	}

	backend, err := r.computeAPI.FindBackendServiceWithMIG(ctx, r.projectID, group)

	if err != nil {
		return emptyInfo, err
	}

	return clusterInfo{group, template, backend}, nil
}

func (r *Runner) scale(ctx context.Context, info clusterInfo) error {
	var primaryTemplate string
	var oldCanarySize int64
	var newCanarySize int64

	// TODO should this reset to zero on the very first scale() call, even if a
	// canary template is found?
	for _, version := range info.group.Versions {
		if version.InstanceTemplate == info.template.SelfLink {
			if version.TargetSize.Fixed > 0 {
				oldCanarySize = version.TargetSize.Fixed

				log.Printf("found existing canary deployment with %d instances", oldCanarySize)
			}

			continue
		}

		if version.Name != "canary" {
			if primaryTemplate != "" {
				return errors.Errorf(
					"found two non-canary templates: '%s' and '%s', cannot determine primary template",
					primaryTemplate,
					version.InstanceTemplate,
				)
			}

			primaryTemplate = version.InstanceTemplate
		}
	}

	if primaryTemplate == "" {
		return errors.New("could not find primary (non-canary) template")
	}

	if oldCanarySize == 0 {
		newCanarySize = 1
	} else {
		newCanarySize = oldCanarySize * 2
	}

	var versions []*compute.InstanceGroupManagerVersion

	if newCanarySize >= info.group.TargetSize {
		newCanarySize = info.group.TargetSize

		versions = []*compute.InstanceGroupManagerVersion{
			&compute.InstanceGroupManagerVersion{
				InstanceTemplate: info.template.SelfLink,
			},
		}
	} else {
		versions = []*compute.InstanceGroupManagerVersion{
			&compute.InstanceGroupManagerVersion{
				InstanceTemplate: primaryTemplate,
			},
			&compute.InstanceGroupManagerVersion{
				Name:             "canary",
				InstanceTemplate: info.template.SelfLink,
				TargetSize:       &compute.FixedOrPercent{Fixed: newCanarySize},
			},
		}
	}

	log.Printf("patching managed instance group with canary target of %d instances", newCanarySize)

	maxSurge := newCanarySize - oldCanarySize
	maxUnavailable := int64(0)

	if maxSurge < int64(len(info.group.DistributionPolicy.Zones)) {
		// avoids 'Fixed updatePolicy.maxSurge for regional managed instance
		// group has to be either 0 or at least equal to the number of zones.'
		// errors
		maxSurge = int64(len(info.group.DistributionPolicy.Zones))
	}

	patch := &compute.InstanceGroupManager{
		Versions: versions,
		UpdatePolicy: &compute.InstanceGroupManagerUpdatePolicy{
			Type:           "PROACTIVE",
			MaxSurge:       &compute.FixedOrPercent{Fixed: maxSurge},
			MaxUnavailable: &compute.FixedOrPercent{Fixed: maxUnavailable},
		},
	}

	if err := r.computeAPI.PatchMIG(ctx, r.projectID, r.location, r.migName, patch); err != nil {
		return errors.Wrap(err, "error updating instance templates in instance group")
	}

	return nil
}

func (r *Runner) waitUntilStable(ctx context.Context, info clusterInfo) (clusterInfo, error) {
	var emptyInfo clusterInfo

	log.Printf("beginning wait until stable loop")

	for ticks := 0; ticks < maxTicks; ticks++ {
		refreshedGroup, err := r.computeAPI.GetMIG(ctx, r.projectID, r.location, r.migName)

		if err != nil {
			return emptyInfo, err
		}

		if refreshedGroup.Status.IsStable && refreshedGroup.Status.VersionTarget.IsReached {
			log.Printf("cluster is stable")

			return clusterInfo{refreshedGroup, info.template, info.backend}, nil
		}

		log.Printf("cluster is still not stable, sleeping")

		r.sleepFunc(tickInterval)
	}

	return emptyInfo, errors.Errorf("cluster did not become stable within %d ticks", maxTicks)
}

func (r *Runner) checkBackendServiceHealth(ctx context.Context, info clusterInfo) error {
	log.Printf("checking backend service health")

	healthResponse, err := r.computeAPI.GetBackendServiceGroupHealth(ctx, r.projectID, info.backend, info.group)

	if err != nil {
		return err
	}

	isUnhealthy := make(map[string]bool)

	for _, healthStatus := range healthResponse.HealthStatus {
		if healthStatus.HealthState == "UNHEALTHY" {
			isUnhealthy[healthStatus.Instance] = true
		}
	}

	log.Printf("found %d unhealthy instances", len(isUnhealthy))

	instances, err := r.computeAPI.GetMIGInstances(ctx, r.projectID, r.location, r.migName)

	if err != nil {
		return err
	}

	for _, instance := range instances {
		if instance.Version.InstanceTemplate == info.template.SelfLink && isUnhealthy[instance.Instance] {
			return errors.Errorf("found unhealthy canary instance in backend service: '%s'", instance.Instance)
		}
	}

	return nil
}

func (r *Runner) isDone(info clusterInfo) bool {
	if len(info.group.Versions) == 1 && info.group.Versions[0].InstanceTemplate == info.template.SelfLink {
		return true
	}

	return false
}
