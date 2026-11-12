package application

// Executable and private-key paths are deployment-owned; the platform cannot
// turn a configuration field into an arbitrary program or filesystem read.
type NativeRDPOptions struct {
	Address, Advertised, Worker, Certificate, PrivateKey, FFmpeg string
	ContainerProfile                                             bool
}

// Configure before starting operational services. Isolated standalone protocol
// fixtures may continue to use StartNativeRDP without the managed listener.
func (a *App) ConfigureNativeRDP(options NativeRDPOptions) {
	a.managedRDP = &options
	a.managedRDPSlots = make(chan struct{}, 2)
}
