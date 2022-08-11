package decoo

import (
	"crypto/ed25519"
	"fmt"
	"strconv"
	"strings"
	"time"

	tmconfig "github.com/tendermint/tendermint/config"
	tmed25519 "github.com/tendermint/tendermint/crypto/ed25519"
	tendermintlog "github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/privval"
	tmtypes "github.com/tendermint/tendermint/types"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/iotaledger/hive.go/core/logger"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo/types"
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

	rootDir := Parameters.Tendermint.Root
	tmconfig.EnsureRoot(rootDir)
	conf := tmconfig.DefaultConfig().SetRoot(rootDir)
	conf.P2P.ListenAddress = "tcp://" + Parameters.Tendermint.BindAddress

	// consensus parameters
	conf.Consensus.CreateEmptyBlocks = Parameters.Tendermint.Consensus.CreateEmptyBlocks
	conf.Consensus.CreateEmptyBlocksInterval = Parameters.Tendermint.Consensus.CreateEmptyBlocksInterval
	conf.Consensus.TimeoutCommit = Parameters.Tendermint.Consensus.BlockInterval
	conf.Consensus.SkipTimeoutCommit = Parameters.Tendermint.Consensus.SkipBlockTimeout

	// private validator
	privValKeyFile := conf.PrivValidatorKeyFile()
	privValStateFile := conf.PrivValidatorStateFile()
	if fileExists(privValKeyFile) && fileExists(privValStateFile) {
		_ = privval.LoadFilePV(privValKeyFile, privValStateFile)
		log.Infow("Found private validator", "keyFile", privValKeyFile,
			"stateFile", privValStateFile)
	} else {
		pv := privval.NewFilePV(privKey, privValKeyFile, privValStateFile)
		pv.Save()

		log.Infow("Generated private validator", "keyFile", privValKeyFile, "stateFile", privValStateFile)
	}

	nodeKeyFile := conf.NodeKeyFile()
	if fileExists(nodeKeyFile) {
		if _, err := p2p.LoadNodeKey(nodeKeyFile); err != nil {
			return nil, nil, fmt.Errorf("invalid node key: %w", err)
		}

		log.Infow("Found node key", "path", nodeKeyFile)
	} else {
		// TODO: should the Tendermint node key (message authentication) be different from our signing key
		nodeKey := p2p.NodeKey{PrivKey: privKey}
		if err := nodeKey.SaveAs(nodeKeyFile); err != nil {
			return nil, nil, fmt.Errorf("failed to save key file: %w", err)
		}

		log.Infow("Generated node key", "path", nodeKeyFile)
	}

	ownPubKey := privKey.PubKey()
	var genesisValidators []tmtypes.GenesisValidator
	var peers []string
	for name, validator := range Parameters.Tendermint.Validators {
		var pubKeyBytes types.Byte32
		if err := pubKeyBytes.Set(validator.PubKey); err != nil {
			return nil, nil, fmt.Errorf("invalid pubKey for tendermint validator %s: %w", strconv.Quote(name), err)
		}
		pubKey := tmed25519.PubKey(pubKeyBytes[:])
		genesisValidators = append(genesisValidators, tmtypes.GenesisValidator{
			PubKey: pubKey,
			Power:  validator.Power,
			Name:   name,
		})
		if !ownPubKey.Equals(pubKey) {
			nodeID := p2p.PubKeyToID(pubKey)
			peers = append(peers, p2p.IDAddressString(nodeID, validator.Address))
		}
	}

	conf.P2P.PersistentPeers = strings.Join(peers, ",")
	// Forcing the max number of connections to be equal to the configured persistent peers (e.g., the entire committee)
	conf.P2P.MaxNumInboundPeers = len(peers)
	conf.P2P.MaxNumOutboundPeers = len(peers)
	// Disable Peer Exchange Reactor
	conf.P2P.PexReactor = false

	// Prometheus configuration
	conf.Instrumentation.Prometheus = Parameters.Tendermint.Prometheus.Enabled
	conf.Instrumentation.PrometheusListenAddr = Parameters.Tendermint.Prometheus.BindAddress

	gen := &tmtypes.GenesisDoc{
		GenesisTime:   time.Unix(Parameters.Tendermint.GenesisTime, 0),
		ChainID:       Parameters.Tendermint.ChainID,
		InitialHeight: 0,
		Validators:    genesisValidators,
	}
	if err := gen.ValidateAndComplete(); err != nil {
		return nil, nil, fmt.Errorf("invalid genesis config: %w", err)
	}
	return conf, gen, nil
}
