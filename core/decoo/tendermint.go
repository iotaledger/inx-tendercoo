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

func loadTendermintConfig(consensusPrivateKey ed25519.PrivateKey, nodePrivateKey ed25519.PrivateKey, networkName string) (*tmconfig.Config, *tmtypes.GenesisDoc, error) {
	log := CoreComponent.Logger()
	tmConsensusKey := tmed25519.PrivKey(consensusPrivateKey)

	rootDir := Parameters.Tendermint.Root
	tmconfig.EnsureRoot(rootDir)
	conf := tmconfig.DefaultConfig().SetRoot(rootDir)
	conf.P2P.ListenAddress = "tcp://" + Parameters.Tendermint.BindAddress

	// consensus parameters
	conf.Consensus.TimeoutCommit = Parameters.Tendermint.Consensus.BlockInterval
	conf.Consensus.SkipTimeoutCommit = Parameters.Tendermint.Consensus.SkipBlockTimeout
	conf.Consensus.CreateEmptyBlocks = Parameters.Tendermint.Consensus.CreateEmptyBlocks
	conf.Consensus.CreateEmptyBlocksInterval = Parameters.Tendermint.Consensus.CreateEmptyBlocksInterval

	// mempool parameters
	// limit the mempool to at most 1000 transactions of up to 1 KB
	conf.Mempool.Size = 1000
	conf.Mempool.MaxTxBytes = 1024
	conf.Mempool.MaxTxsBytes = int64(conf.Mempool.Size) * int64(conf.Mempool.MaxTxBytes)

	// private validator
	privValKeyFile := conf.PrivValidatorKeyFile()
	privValStateFile := conf.PrivValidatorStateFile()
	if fileExists(privValKeyFile) && fileExists(privValStateFile) {
		_ = privval.LoadFilePV(privValKeyFile, privValStateFile)
		log.Infow("Found private validator", "keyFile", privValKeyFile,
			"stateFile", privValStateFile)
	} else {
		pv := privval.NewFilePV(tmConsensusKey, privValKeyFile, privValStateFile)
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
		nodeKey := p2p.NodeKey{PrivKey: tmed25519.PrivKey(nodePrivateKey)}
		if err := nodeKey.SaveAs(nodeKeyFile); err != nil {
			return nil, nil, fmt.Errorf("failed to save key file: %w", err)
		}

		log.Infow("Generated node key", "path", nodeKeyFile)
	}

	var genesisValidators []tmtypes.GenesisValidator
	for name, validator := range Parameters.Tendermint.Validators {
		var publicKeyBytes types.Byte32
		if err := publicKeyBytes.Set(validator.PublicKey); err != nil {
			return nil, nil, fmt.Errorf("invalid public key for validator %s: %w", strconv.Quote(name), err)
		}
		pubKey := tmed25519.PubKey(publicKeyBytes[:])
		genesisValidators = append(genesisValidators, tmtypes.GenesisValidator{
			PubKey: pubKey,
			Power:  validator.Power,
			Name:   name,
		})
	}

	var peers []string
	id := p2p.PubKeyToID(tmConsensusKey.PubKey())
	for _, peer := range Parameters.Tendermint.Peers {
		addr, err := p2p.NewNetAddressString(peer)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid address in peers: %w", err)
		}

		// only add the address as a peer, if it does not belong to ourselves
		if id != addr.ID {
			peers = append(peers, peer)
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

	if Parameters.Tendermint.ChainID != networkName {
		return nil, nil, fmt.Errorf("chain ID must match the network name %s", strconv.Quote(networkName))
	}
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
