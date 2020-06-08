package bulldozer

type Cmd struct {
	Start StartCmd `cmd help:"Start a managed canary deploy."`
}
