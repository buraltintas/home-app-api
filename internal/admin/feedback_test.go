package admin

import "testing"

func TestValidateFeedbackReply(t *testing.T) {
	reply, err := validateFeedbackReply("  Düzelttik, tekrar dener misin?  ")
	if err != nil || reply != "Düzelttik, tekrar dener misin?" {
		t.Fatalf("reply=%q err=%v", reply, err)
	}
	if _, err = validateFeedbackReply("   "); err == nil {
		t.Fatal("empty reply accepted")
	}
	tooLong := make([]rune, 4001)
	for i := range tooLong {
		tooLong[i] = 'a'
	}
	if _, err = validateFeedbackReply(string(tooLong)); err == nil {
		t.Fatal("overlong reply accepted")
	}
}
