// Package tendermint contains the protobuf definitions for Tendermint transactions.
package tendermint

// generate protobuf sources
//go:generate protoc --go_out=paths=source_relative:. tx.proto
