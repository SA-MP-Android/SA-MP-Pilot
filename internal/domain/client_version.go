package domain

// ClientVersion identifies one of the supported SA-MP wire profiles.
type ClientVersion string

const (
	Version037R4  ClientVersion = "0.3.7-R4"
	Version03DLR1 ClientVersion = "0.3.DL-R1"
)

func NormalizeClientVersion(version ClientVersion) (ClientVersion, bool) {
	if version == "" {
		return Version037R4, true
	}
	return version, version == Version037R4 || version == Version03DLR1
}
