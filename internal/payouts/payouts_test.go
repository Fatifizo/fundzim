package payouts

import "testing"

func TestNormaliseMobileWallet(t *testing.T) {
	canon, suffix, det := normalise("ECOCASH", "077 123 4567", "")
	if len(det) != 0 || canon != "+263771234567" || suffix != "567" {
		t.Fatalf("valid wallet: %q %q %v", canon, suffix, det)
	}
	for _, bad := range []string{"12345", "+447700900123", "0242700000", "not a number"} {
		if _, _, det := normalise("ECOCASH", bad, ""); len(det) == 0 {
			t.Errorf("%q accepted as a Zimbabwean mobile wallet", bad)
		}
	}
}

func TestNormaliseBankAccount(t *testing.T) {
	canon, suffix, det := normalise("BANK_TRANSFER", "1234-5678 9012", "CBZ")
	if len(det) != 0 || canon != "123456789012" || suffix != "9012" {
		t.Fatalf("valid account: %q %q %v", canon, suffix, det)
	}
	if _, _, det := normalise("BANK_TRANSFER", "12AB", ""); len(det) != 2 {
		t.Fatalf("invalid account and bank code: %v", det)
	}
	if _, _, det := normalise("PAYPAL", "x", ""); len(det) != 1 || det[0].Field != "rail" {
		t.Fatalf("unknown rail: %v", det)
	}
}
