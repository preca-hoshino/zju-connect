package binding

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mythologyli/zju-connect/client/authchallenge"
)

// answerChallenge replies on a background goroutine so the blocking challenge
// handler can complete.
func answerChallenge(t *testing.T, handler *challengeHandler, wantKind string, check func(Challenge), answer string) {
	t.Helper()
	done := make(chan struct{})
	responder := func(challenge Challenge) {
		if challenge.Kind != wantKind {
			t.Errorf("challenge kind = %q, want %q", challenge.Kind, wantKind)
		}
		if check != nil {
			check(challenge)
		}
		go func() {
			if !handler.Respond(challenge.ID, answer) {
				t.Error("Respond() = false, want true")
			}
		}()
		close(done)
	}
	handler.respond = responder
}

func TestChallengeHandlerCodeRoundTrip(t *testing.T) {
	handler := newChallengeHandler(nil)
	answerChallenge(t, handler, ChallengeCode, func(challenge Challenge) {
		var payload codeChallengePayload
		if err := json.Unmarshal([]byte(challenge.Payload), &payload); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		if payload.Kind != string(authchallenge.CodeSMS) {
			t.Errorf("payload.Kind = %q, want sms", payload.Kind)
		}
		if !payload.CanSkipSecondaryAuth {
			t.Error("payload.CanSkipSecondaryAuth = false, want true")
		}
	}, `{"code":"123456","skip_secondary_auth":true}`)

	response, err := handler.HandleCodeChallenge(authchallenge.CodeChallenge{
		Kind:                 authchallenge.CodeSMS,
		Message:              "Enter code",
		CanSkipSecondaryAuth: true,
	})
	if err != nil {
		t.Fatalf("HandleCodeChallenge() error = %v", err)
	}
	if response.Code != "123456" || !response.SkipSecondaryAuth {
		t.Fatalf("response = %+v, want code 123456 with skip", response)
	}
}

func TestChallengeHandlerCodeErrorIsPropagated(t *testing.T) {
	handler := newChallengeHandler(nil)
	answerChallenge(t, handler, ChallengeCode, nil, `{"error":"user cancelled"}`)

	_, err := handler.HandleCodeChallenge(authchallenge.CodeChallenge{Kind: authchallenge.CodeTOTP})
	if err == nil || err.Error() != "user cancelled" {
		t.Fatalf("HandleCodeChallenge() error = %v, want user cancelled", err)
	}
}

func TestChallengeHandlerTextCaptchaEncodesImage(t *testing.T) {
	handler := newChallengeHandler(nil)
	answerChallenge(t, handler, ChallengeTextCaptcha, func(challenge Challenge) {
		var payload captchaChallengePayload
		if err := json.Unmarshal([]byte(challenge.Payload), &payload); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		if payload.Image != "AQID" { // base64 of {1,2,3}
			t.Errorf("payload.Image = %q, want AQID", payload.Image)
		}
	}, `{"code":"XY12"}`)

	response, err := handler.HandleTextCaptcha(authchallenge.TextCaptchaChallenge{Image: []byte{1, 2, 3}})
	if err != nil {
		t.Fatalf("HandleTextCaptcha() error = %v", err)
	}
	if response.Code != "XY12" {
		t.Fatalf("response.Code = %q, want XY12", response.Code)
	}
}

func TestChallengeHandlerClickCaptchaDecodesPoints(t *testing.T) {
	handler := newChallengeHandler(nil)
	answerChallenge(t, handler, ChallengeClickCaptcha, nil, `{"points":[{"x":5,"y":6}],"width":100,"height":40}`)

	response, err := handler.HandleClickCaptcha(authchallenge.ClickCaptchaChallenge{})
	if err != nil {
		t.Fatalf("HandleClickCaptcha() error = %v", err)
	}
	if len(response.Points) != 1 || response.Points[0].X != 5 || response.Points[0].Y != 6 {
		t.Fatalf("response.Points = %+v, want one point at (5,6)", response.Points)
	}
	if response.Width != 100 || response.Height != 40 {
		t.Fatalf("response size = %dx%d, want 100x40", response.Width, response.Height)
	}
}

func TestChallengeHandlerExternalLogin(t *testing.T) {
	handler := newChallengeHandler(nil)
	answerChallenge(t, handler, ChallengeExternalLogin, func(challenge Challenge) {
		var payload externalLoginChallengePayload
		if err := json.Unmarshal([]byte(challenge.Payload), &payload); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		if payload.LoginURL != "https://idp.example/login" {
			t.Errorf("payload.LoginURL = %q", payload.LoginURL)
		}
	}, `{"callback_url":"https://vpn/callback?code=abc"}`)

	response, err := handler.HandleExternalLogin(authchallenge.ExternalLoginChallenge{
		Kind:     authchallenge.ExternalLoginOAuth2,
		LoginURL: "https://idp.example/login",
	})
	if err != nil {
		t.Fatalf("HandleExternalLogin() error = %v", err)
	}
	if response.CallbackURL != "https://vpn/callback?code=abc" {
		t.Fatalf("response.CallbackURL = %q", response.CallbackURL)
	}
}

func TestChallengeHandlerRejectsUnknownID(t *testing.T) {
	handler := newChallengeHandler(nil)
	if handler.Respond(9999, "{}") {
		t.Fatal("Respond(9999) = true, want false for unknown id")
	}
}

func TestChallengeHandlerFailAllUnblocksWaiters(t *testing.T) {
	handler := newChallengeHandler(func(Challenge) {})
	done := make(chan struct{})
	go func() {
		_, _ = handler.HandleCodeChallenge(authchallenge.CodeChallenge{Kind: authchallenge.CodeTOTP})
		close(done)
	}()

	deadline := time.After(2 * time.Second)
	for {
		handler.mu.Lock()
		pending := len(handler.pending)
		handler.mu.Unlock()
		if pending > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("challenge never registered")
		case <-time.After(time.Millisecond):
		}
	}

	handler.failAll()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("HandleCodeChallenge did not unblock after failAll")
	}
}
