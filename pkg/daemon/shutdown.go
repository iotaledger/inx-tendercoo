package daemon

const (
	PriorityDisconnectINX = iota // no dependencies
	PriorityStopMigrator
	PriorityStopCoordinator
	PriorityStopCoordinatorMilestoneTicker
)
