package agency

type OffChainTransmission interface {
	Start() error

	VRF(add []byte) ([]byte, error)

	Stop() error
}
