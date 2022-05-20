package decoo

import (
	"crypto/ed25519"
	"fmt"
	"time"

	"github.com/iotaledger/hive.go/logger"
	tmconfig "github.com/tendermint/tendermint/config"
	tmed25519 "github.com/tendermint/tendermint/crypto/ed25519"
	tendermintlog "github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/privval"
	tmtypes "github.com/tendermint/tendermint/types"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// TenderLogger is a simple wrapper for the Tendermint logger.
type TenderLogger struct {
	*logger.Logger
	level zap.AtomicLevel
}

// Debug logs a message with some additional context. The variadic key-value pairs are treated as they are in With.
func (l TenderLogger) Debug(msg string, keyVals ...interface{}) {
	if l.level.Enabled(zap.DebugLevel) {
		l.Logger.Debugw(msg, keyVals...)
	}
}

// Info logs a message with some additional context. The variadic key-value pairs are treated as they are in With.
func (l TenderLogger) Info(msg string, keyVals ...interface{}) {
	if l.level.Enabled(zap.InfoLevel) {
		l.Logger.Infow(msg, keyVals...)
	}
}

// Error logs a message with some additional context. The variadic key-value pairs are treated as they are in With.
func (l TenderLogger) Error(msg string, keyVals ...interface{}) {
	if l.level.Enabled(zap.ErrorLevel) {
		l.Logger.Errorw(msg, keyVals...)
	}
}

// With adds a variadic number of key-value pairs to the logging context.
func (l TenderLogger) With(keyVals ...interface{}) tendermintlog.Logger {
	return TenderLogger{l.Logger.With(keyVals...), l.level}
}

// NewTenderLogger creates a new Tendermint compatible logger.
func NewTenderLogger(log *logger.Logger, l zapcore.Level) TenderLogger {
	return TenderLogger{log.Desugar().WithOptions(zap.AddCallerSkip(1)).Sugar(), zap.NewAtomicLevelAt(l)}
}

func loadTendermintConfig(priv ed25519.PrivateKey) (*tmconfig.Config, *tmtypes.GenesisDoc, error) {
	log := CoreComponent.Logger()
	privKey := tmed25519.PrivKey(priv)

	rootDir := ParamsCoordinator.Tendermint.Root
	tmconfig.EnsureRoot(rootDir)
	conf := tmconfig.DefaultValidatorConfig().SetRoot(rootDir)

	conf.Consensus.CreateEmptyBlocks = ParamsCoordinator.Tendermint.CreateEmptyBlocks
	// TODO: make other Tendermint options configurable

	// private validator
	privValKeyFile := conf.PrivValidator.KeyFile()
	privValStateFile := conf.PrivValidator.StateFile()
	if fileExists(privValKeyFile) {
		if _, err := privval.LoadFilePV(privValKeyFile, privValStateFile); err != nil {
			return nil, nil, fmt.Errorf("invalid private validator: %w", err)
		}

		log.Infow("Found private validator", "keyFile", privValKeyFile,
			"stateFile", privValStateFile)
	} else {
		pv := privval.NewFilePV(privKey, privValKeyFile, privValStateFile)
		pv.Save()

		log.Infow("Generated private validator", "keyFile", privValKeyFile, "stateFile", privValStateFile)
	}

	nodeKeyFile := conf.NodeKeyFile()
	if fileExists(nodeKeyFile) {
		if _, err := tmtypes.LoadNodeKey(nodeKeyFile); err != nil {
			return nil, nil, fmt.Errorf("invalid node key: %w", err)
		}

		log.Infow("Found node key", "path", nodeKeyFile)
	} else {
		// TODO: should the Tendermint node key (message authentication) be different from our signing key
		nodeKey := tmtypes.NodeKey{
			ID:      tmtypes.NodeIDFromPubKey(privKey.PubKey()),
			PrivKey: privKey,
		}
		if err := nodeKey.SaveAs(nodeKeyFile); err != nil {
			return nil, nil, fmt.Errorf("failed to save key file: %w", err)
		}

		log.Infow("Generated node key", "path", nodeKeyFile)
	}

	var genesisValidators []tmtypes.GenesisValidator
	for name, validator := range ParamsCoordinator.Tendermint.Validators {
		genesisValidators = append(genesisValidators, tmtypes.GenesisValidator{
			PubKey: tmed25519.PubKey(validator.PubKey[:]),
			Power:  validator.Power,
			Name:   name,
		})
	}

	gen := &tmtypes.GenesisDoc{
		GenesisTime:   time.Unix(ParamsCoordinator.Tendermint.GenesisTime, 0),
		ChainID:       ParamsCoordinator.Tendermint.ChainID,
		InitialHeight: 0,
		Validators:    genesisValidators,
	}
	if err := gen.ValidateAndComplete(); err != nil {
		return nil, nil, fmt.Errorf("invalid genesis config: %w", err)
	}
	return conf, gen, nil
}
