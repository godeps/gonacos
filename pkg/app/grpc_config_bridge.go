package app

// errConfigBridgeNotReady is returned when the gRPC adapter is called before
// the config service has been wired. The name is retained for compatibility
// even though the adapter now holds a direct service reference.
var errConfigBridgeNotReady = &bridgeError{"config service not ready"}

type bridgeError struct{ msg string }

func (e *bridgeError) Error() string { return e.msg }
