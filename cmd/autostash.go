package cmd

import (
	"errors"

	"github.com/spf13/cobra"

	"stk/internal/config"
)

// autostashPref is the --autostash/--no-autostash pair.
//
// Neither flag is a mere boolean: with both absent the repository's
// stk.autostash setting decides, so --no-autostash has to be able to say no to
// a standing yes.
type autostashPref struct {
	on  bool
	off bool
}

// register adds the pair to a command.
//
// Neither gets a shorthand. Parking and unparking someone's uncommitted work
// is not a thing a slipped letter should switch on or off.
func (p *autostashPref) register(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&p.on, "autostash", false,
		"stash uncommitted changes for the duration and restore them afterwards")
	cmd.Flags().BoolVar(&p.off, "no-autostash", false,
		"leave uncommitted changes alone even when "+config.KeyAutostash+" is set")
}

// apply resolves the flags against the repository setting.
func (p *autostashPref) apply(a *app) error {
	if p.on && p.off {
		return errors.New("use either --autostash or --no-autostash, not both")
	}
	switch {
	case p.on:
		a.Env.Autostash = true
	case p.off:
		a.Env.Autostash = false
	default:
		a.Env.Autostash = a.Cfg.Autostash
	}
	return nil
}
