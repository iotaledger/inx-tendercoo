package app

import (
	cryptorand "crypto/rand"
	"encoding/binary"
	mathrand "math/rand"

	"github.com/iotaledger/hive.go/app"
	"github.com/iotaledger/hive.go/app/components/profiling"
	"github.com/iotaledger/hive.go/app/components/shutdown"
	"github.com/iotaledger/inx-app/components/inx"
	"github.com/iotaledger/inx-tendercoo/components/decoo"
)

var (
	// Name of the app.
	Name = "inx-tendercoo"

	// Version of the app.
	Version = "1.0.0"
)

func init() {
	// seed math/rand with something cryptographically secure
	var b [8]byte
	if _, err := cryptorand.Read(b[:]); err != nil {
		panic("cannot seed math/rand package with cryptographically secure random number generator")
	}

	//nolint: staticcheck, (mathrand.Seed is deprecated)
	mathrand.Seed(int64(binary.LittleEndian.Uint64(b[:])))
}

var initComponent = &app.InitComponent{
	Component: &app.Component{
		Name: "App",
	},
	NonHiddenFlags: []string{
		"config",
		"help",
		"version",
		decoo.CfgCoordinatorBootstrap,
		decoo.CfgCoordinatorStartIndex,
		decoo.CfgCoordinatorStartMilestoneID,
		decoo.CfgCoordinatorStartMilestoneBlockID,
	},
}

func App() *app.App {
	return app.New(Name, Version,
		app.WithInitComponent(initComponent),
		app.WithComponents(
			inx.Component,
			decoo.Component,
			shutdown.Component,
			profiling.Component,
		),
	)
}
