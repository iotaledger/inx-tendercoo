package daemon

const (
	PriorityDisconnectINX = iota
	PriorityStopTangleListener
	PriorityStopTreasuryListener
	PriorityStopMigrator
	PriorityStopCoordinator
	PriorityStopCoordinatorMilestoneTicker
)
