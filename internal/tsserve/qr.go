// A QR code of the published URL, for the phone: drawn here from the URL alone (rsc.io/qr, no
// network, no third party), as a PNG data URL the card shows (the CSP allows data: images).

package tsserve

import (
	"encoding/base64"

	"rsc.io/qr"
)

// QRDataURL encodes text as a QR code PNG data URL, or "" when it cannot.
func QRDataURL(text string) string {
	if text == "" {
		return ""
	}
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return ""
	}
	code.Scale = 8
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(code.PNG())
}
