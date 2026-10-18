package domain

import "strings"

// Microphone represents the KA-1333 USB microphone entity,
// its post-DSP level [0%..100%], and Dual-Mute state (virtual + physical).
type Microphone struct {
	VendorID        string          `json:"vendor_id"`
	ProductID       string          `json:"product_id"`
	Serial          string          `json:"serial"`
	ProfileName     string          `json:"profile_name"`
	VirtualNodeName string          `json:"virtual_node_name"`
	BasePreset      string          `json:"base_preset"`
	PostDSPLevel    PercentageLevel `json:"post_dsp_level"`
	VirtualMuted    bool            `json:"virtual_muted"`
	PhysicalMuted   bool            `json:"physical_muted"`
}

// NewDefaultKA1333 returns the default calibrated KA-1333 microphone.
func NewDefaultKA1333() *Microphone {
	return &Microphone{
		VendorID:        "0x0a67",
		ProductID:       "0xd117",
		Serial:          "MU900_KA-1333_20190805V001",
		ProfileName:     "iec958-stereo",
		VirtualNodeName: "easyeffects_source",
		BasePreset:      "Preset KA-1333",
		PostDSPLevel:    NewPercentageLevel(100),
		VirtualMuted:    false,
		PhysicalMuted:   false,
	}
}

// IsPrivacyMuted returns true only when BOTH virtual and physical channels are muted.
func (m Microphone) IsPrivacyMuted() bool {
	return m.VirtualMuted && m.PhysicalMuted
}

// HasMuteDivergence indicates whether virtual and physical mute states differ.
func (m Microphone) HasMuteDivergence() bool {
	return m.VirtualMuted != m.PhysicalMuted
}

// SetDualMute sets both virtual and physical mute states.
func (m *Microphone) SetDualMute(muted bool) {
	m.VirtualMuted = muted
	m.PhysicalMuted = muted
}

// MatchesUSBDevice checks if device properties match the expected hardware identity.
func (m Microphone) MatchesUSBDevice(vendorID, productID, serial string) bool {
	return strings.EqualFold(strings.TrimSpace(vendorID), m.VendorID) &&
		strings.EqualFold(strings.TrimSpace(productID), m.ProductID) &&
		strings.TrimSpace(serial) == m.Serial
}
