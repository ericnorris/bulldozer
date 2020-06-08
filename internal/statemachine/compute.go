package statemachine

import (
	"context"

	"github.com/pkg/errors"
	"google.golang.org/api/compute/v1"
)

type RegionOrZone struct {
	Region string
	Zone   string
}

func Region(region string) RegionOrZone {
	return RegionOrZone{Region: region}
}

func Zone(zone string) RegionOrZone {
	return RegionOrZone{Zone: zone}
}

type computeAPI interface {
	GetMIG(
		ctx context.Context, projectID string, location RegionOrZone, name string,
	) (*compute.InstanceGroupManager, error)

	GetMIGInstances(
		ctx context.Context, projectID string, location RegionOrZone, name string,
	) ([]*compute.ManagedInstance, error)

	PatchMIG(
		ctx context.Context, projectID string, location RegionOrZone, name string, patch *compute.InstanceGroupManager,
	) error

	GetInstanceTemplate(ctx context.Context, projectID, name string) (*compute.InstanceTemplate, error)

	FindBackendServiceWithMIG(
		ctx context.Context, projectID string, mig *compute.InstanceGroupManager,
	) (*compute.BackendService, error)

	GetBackendServiceGroupHealth(
		ctx context.Context, projectID string, backend *compute.BackendService, mig *compute.InstanceGroupManager,
	) (*compute.BackendServiceGroupHealth, error)
}

var _ computeAPI = (*googleComputeAPI)(nil)

type googleComputeAPI struct {
	computeService *compute.Service
}

func newGoogleComputeAPI(ctx context.Context) (*googleComputeAPI, error) {
	computeService, err := compute.NewService(ctx)

	if err != nil {
		return nil, errors.Wrap(err, "error initializing Google Compute API client")
	}

	return &googleComputeAPI{computeService}, nil
}

func (g *googleComputeAPI) GetMIG(
	ctx context.Context,
	projectID string, location RegionOrZone, name string,
) (*compute.InstanceGroupManager, error) {
	var mig *compute.InstanceGroupManager
	var err error

	switch {
	case location.Region != "":
		mig, err = g.computeService.
			RegionInstanceGroupManagers.
			Get(projectID, location.Region, name).
			Context(ctx).
			Do()

	case location.Zone != "":
		mig, err = g.computeService.
			InstanceGroupManagers.
			Get(projectID, location.Zone, name).
			Context(ctx).
			Do()

	default:
		err = errors.New("must specify either region or zone")
	}

	if err != nil {
		return nil, errors.Wrap(err, "error retrieving instance group information")
	}

	return mig, nil
}

func (g *googleComputeAPI) GetMIGInstances(
	ctx context.Context,
	projectID string, location RegionOrZone, name string,
) ([]*compute.ManagedInstance, error) {
	var instances []*compute.ManagedInstance
	var err error

	switch {
	case location.Region != "":
		err = g.computeService.
			RegionInstanceGroupManagers.
			ListManagedInstances(projectID, location.Region, name).
			Pages(ctx, func(response *compute.RegionInstanceGroupManagersListInstancesResponse) error {
				instances = append(instances, response.ManagedInstances...)

				return nil
			})

	case location.Zone != "":
		err = g.computeService.
			InstanceGroupManagers.
			ListManagedInstances(projectID, location.Zone, name).
			Pages(ctx, func(response *compute.InstanceGroupManagersListManagedInstancesResponse) error {
				instances = append(instances, response.ManagedInstances...)

				return nil
			})

	default:
		err = errors.New("must specify either region or zone")
	}

	if err != nil {
		return nil, errors.Wrap(err, "error retrieving list of managed instances")
	}

	return instances, nil
}

func (g *googleComputeAPI) PatchMIG(
	ctx context.Context,
	projectID string, location RegionOrZone, name string,
	patch *compute.InstanceGroupManager,
) error {
	var err error

	switch {
	case location.Region != "":
		_, err = g.computeService.
			RegionInstanceGroupManagers.
			Patch(projectID, location.Region, name, patch).
			Context(ctx).
			Do()

	case location.Zone != "":
		_, err = g.computeService.
			InstanceGroupManagers.
			Patch(projectID, location.Zone, name, patch).
			Context(ctx).
			Do()

	default:
		err = errors.New("must specify either region or zone")
	}

	if err != nil {
		return errors.Wrap(err, "error retrieving instance group information")
	}

	return nil
}

func (g *googleComputeAPI) GetInstanceTemplate(
	ctx context.Context,
	projectID, name string,
) (*compute.InstanceTemplate, error) {
	template, err := g.computeService.
		InstanceTemplates.
		Get(projectID, name).
		Context(ctx).
		Do()

	return template, errors.Wrap(err, "error retrieving instance template information")
}

func (g googleComputeAPI) FindBackendServiceWithMIG(
	ctx context.Context,
	projectID string, mig *compute.InstanceGroupManager,
) (*compute.BackendService, error) {
	var backendService *compute.BackendService

	err := g.computeService.
		BackendServices.
		AggregatedList(projectID).
		Pages(ctx, func(aggregate *compute.BackendServiceAggregatedList) error {
			// for every entry in the aggregate list
			for _, list := range aggregate.Items {
				// check the list of backend services
				for _, candidateBackendService := range list.BackendServices {
					// ...by iterating over the backends in the backend service
					for _, backend := range backendService.Backends {
						if backend.Group == mig.SelfLink {
							backendService = candidateBackendService

							return nil
						}
					}
				}
			}

			return nil
		})

	if err != nil {
		return nil, errors.Wrap(err, "error listing backend services in project")
	}

	if backendService == nil {
		return nil, errors.New("could not find backend service containing the specified instance group")
	}

	return backendService, nil
}

func (g googleComputeAPI) GetBackendServiceGroupHealth(
	ctx context.Context,
	projectID string, backend *compute.BackendService, mig *compute.InstanceGroupManager,
) (*compute.BackendServiceGroupHealth, error) {
	var groupHealth *compute.BackendServiceGroupHealth
	var err error

	groupRef := &compute.ResourceGroupReference{Group: mig.SelfLink}

	switch {
	case backend.Region != "":
		groupHealth, err = g.computeService.
			RegionBackendServices.
			GetHealth(projectID, backend.Region, backend.Name, groupRef).
			Context(ctx).
			Do()

	default:
		groupHealth, err = g.computeService.
			BackendServices.
			GetHealth(projectID, backend.Name, groupRef).
			Context(ctx).
			Do()
	}

	if err != nil {
		return nil, errors.Wrap(err, "error getting health of backend service group")
	}

	return groupHealth, nil
}
