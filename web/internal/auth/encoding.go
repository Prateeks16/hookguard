package auth

import "encoding/base64"

// PHC strings use unpadded standard base64 for salt/key segments.
var phcEncoding = base64.RawStdEncoding

func b64(b []byte) string { return phcEncoding.EncodeToString(b) }

func unb64(s string) ([]byte, error) { return phcEncoding.DecodeString(s) }
