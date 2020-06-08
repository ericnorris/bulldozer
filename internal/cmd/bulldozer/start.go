package bulldozer

import (
	"context"

	"github.com/ericnorris/bulldozer/internal/statemachine"
)

type StartCmd struct {
	ProjectID     string `kong:"required,help="Google Cloud Platform project ID."`
	Region        string `kong:"required,help='Region of the regional managed instance group.'"`
	InstanceGroup string `kong:"required,help='Name of the regional managed instance group'"`
	Template      string `kong:"required,help='Name of the instance template to deploy.'"`
}

func (c *StartCmd) Run(ctx context.Context) error {
	runner, err := statemachine.New(ctx, c.ProjectID, statemachine.Region(c.Region), c.InstanceGroup, c.Template)

	if err != nil {
		return err
	}

	return runner.Start(ctx)
}
