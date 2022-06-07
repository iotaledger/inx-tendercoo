package app

import (
	crypto_rand "crypto/rand"
	"encoding/binary"
	math_rand "math/rand"

	"github.com/iotaledger/hive.go/app"
	"github.com/iotaledger/hive.go/app/core/shutdown"
	"github.com/iotaledger/hive.go/app/plugins/profiling"
	"github.com/iotaledger/inx-app/inx"
	"github.com/iotaledger/inx-tendercoo/core/decoo"
)

var (
	// Name of the app.
	Name = "inx-tendercoo"

	// Version of the app.
	Version = "0.0.1"
)

func App() *app.App {
	return app.New(Name, Version,
		app.WithInitComponent(InitComponent),
		app.WithCoreComponents([]*app.CoreComponent{
			inx.CoreComponent,
			decoo.CoreComponent,
			shutdown.CoreComponent,
		}...),
		app.WithPlugins([]*app.Plugin{
			profiling.Plugin,
		}...),
	)
}

var InitComponent = &app.InitComponent{
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

func init() {
	// seed math/rand with something cryptographically secure
	var b [8]byte
	if _, err := crypto_rand.Read(b[:]); err != nil {
		panic("cannot seed math/rand package with cryptographically secure random number generator")
	}
	math_rand.Seed(int64(binary.LittleEndian.Uint64(b[:])))
}
