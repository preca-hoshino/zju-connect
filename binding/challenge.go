package binding

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/mythologyli/zju-connect/client/authchallenge"
)

// Challenge kinds delivered to the host.
const (
	ChallengeCode          = "code"
	ChallengeTextCaptcha   = "text_captcha"
	ChallengeClickCaptcha  = "click_captcha"
	ChallengeExternalLogin = "external_login"
)

// Challenge carries one authentication challenge from the VPN server to the
// host UI. Payload is a JSON object whose schema depends on Kind.
//
// The flow is two-phase because a Dart NativeCallable.listener cannot return a
// value synchronously: the library calls the host's challenge callback, which
// must eventually answer with RespondChallenge(id, ...) from any thread.
type Challenge struct {
	ID      int64
	Kind    string
	Payload string
}

// codeChallengePayload is the payload for ChallengeCode.
type codeChallengePayload struct {
	Kind                 string `json:"kind"`
	Message              string `json:"message"`
	CanSkipSecondaryAuth bool   `json:"can_skip_secondary_auth"`
}

// captchaChallengePayload is shared by the text and click captcha challenges.
type captchaChallengePayload struct {
	Message    string `json:"message"`
	OutputPath string `json:"output_path,omitempty"`
	Image      string `json:"image_base64"`
}

// externalLoginChallengePayload is the payload for ChallengeExternalLogin.
type externalLoginChallengePayload struct {
	Kind     string `json:"kind"`
	LoginURL string `json:"login_url"`
	Message  string `json:"message"`
}

// ChallengeResponder is invoked (from a background goroutine) with a challenge
// that the host must service.
type ChallengeResponder func(challenge Challenge)

// challengeHandler adapts the synchronous authchallenge.Handler interface to
// the asynchronous responder callback. Each challenge is assigned an id and
// the call blocks until Respond is called with that id.
type challengeHandler struct {
	respond ChallengeResponder

	nextID  atomic.Int64
	mu      sync.Mutex
	pending map[int64]chan string
}

func newChallengeHandler(respond ChallengeResponder) *challengeHandler {
	return &challengeHandler{
		respond: respond,
		pending: make(map[int64]chan string),
	}
}

// Respond delivers the host's answer for a challenge. Returning false means the
// id was unknown, already answered, or the session was torn down.
func (h *challengeHandler) Respond(id int64, response string) bool {
	h.mu.Lock()
	ch, ok := h.pending[id]
	if ok {
		delete(h.pending, id)
	}
	h.mu.Unlock()
	if !ok {
		return false
	}
	ch <- response
	return true
}

// failAll unblocks every waiting challenge after the session was closed.
func (h *challengeHandler) failAll() {
	h.mu.Lock()
	pending := h.pending
	h.pending = make(map[int64]chan string)
	h.mu.Unlock()
	for _, ch := range pending {
		ch <- `{"error":"session closed"}`
	}
}

func (h *challengeHandler) ask(kind string, payload any) (string, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode challenge payload: %w", err)
	}

	id := h.nextID.Add(1)
	ch := make(chan string, 1)

	h.mu.Lock()
	h.pending[id] = ch
	h.mu.Unlock()

	h.respond(Challenge{ID: id, Kind: kind, Payload: string(encoded)})
	return <-ch, nil
}

func (h *challengeHandler) HandleCodeChallenge(challenge authchallenge.CodeChallenge) (authchallenge.CodeResponse, error) {
	raw, err := h.ask(ChallengeCode, codeChallengePayload{
		Kind:                 string(challenge.Kind),
		Message:              challenge.Message,
		CanSkipSecondaryAuth: challenge.CanSkipSecondaryAuth,
	})
	if err != nil {
		return authchallenge.CodeResponse{}, err
	}
	var response struct {
		Code              string `json:"code"`
		SkipSecondaryAuth bool   `json:"skip_secondary_auth"`
		Error             string `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		return authchallenge.CodeResponse{}, fmt.Errorf("decode code challenge response: %w", err)
	}
	if response.Error != "" {
		return authchallenge.CodeResponse{}, fmt.Errorf("%s", response.Error)
	}
	return authchallenge.CodeResponse{
		Code:              response.Code,
		SkipSecondaryAuth: response.SkipSecondaryAuth,
	}, nil
}

func (h *challengeHandler) HandleTextCaptcha(challenge authchallenge.TextCaptchaChallenge) (authchallenge.TextCaptchaResponse, error) {
	raw, err := h.ask(ChallengeTextCaptcha, captchaChallengePayload{
		Message:    challenge.Message,
		OutputPath: challenge.OutputPath,
		Image:      base64.StdEncoding.EncodeToString(challenge.Image),
	})
	if err != nil {
		return authchallenge.TextCaptchaResponse{}, err
	}
	var response struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		return authchallenge.TextCaptchaResponse{}, fmt.Errorf("decode text captcha response: %w", err)
	}
	if response.Error != "" {
		return authchallenge.TextCaptchaResponse{}, fmt.Errorf("%s", response.Error)
	}
	return authchallenge.TextCaptchaResponse{Code: response.Code}, nil
}

func (h *challengeHandler) HandleClickCaptcha(challenge authchallenge.ClickCaptchaChallenge) (authchallenge.ClickCaptchaResponse, error) {
	raw, err := h.ask(ChallengeClickCaptcha, captchaChallengePayload{
		Message:    challenge.Message,
		OutputPath: challenge.OutputPath,
		Image:      base64.StdEncoding.EncodeToString(challenge.Image),
	})
	if err != nil {
		return authchallenge.ClickCaptchaResponse{}, err
	}
	var response struct {
		Points []authchallenge.Point `json:"points"`
		Width  int                   `json:"width"`
		Height int                   `json:"height"`
		Error  string                `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		return authchallenge.ClickCaptchaResponse{}, fmt.Errorf("decode click captcha response: %w", err)
	}
	if response.Error != "" {
		return authchallenge.ClickCaptchaResponse{}, fmt.Errorf("%s", response.Error)
	}
	return authchallenge.ClickCaptchaResponse{
		Points: response.Points,
		Width:  response.Width,
		Height: response.Height,
	}, nil
}

func (h *challengeHandler) HandleExternalLogin(challenge authchallenge.ExternalLoginChallenge) (authchallenge.ExternalLoginResponse, error) {
	raw, err := h.ask(ChallengeExternalLogin, externalLoginChallengePayload{
		Kind:     string(challenge.Kind),
		LoginURL: challenge.LoginURL,
		Message:  challenge.Message,
	})
	if err != nil {
		return authchallenge.ExternalLoginResponse{}, err
	}
	var response struct {
		CallbackURL string `json:"callback_url"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		return authchallenge.ExternalLoginResponse{}, fmt.Errorf("decode external login response: %w", err)
	}
	if response.Error != "" {
		return authchallenge.ExternalLoginResponse{}, fmt.Errorf("%s", response.Error)
	}
	return authchallenge.ExternalLoginResponse{CallbackURL: response.CallbackURL}, nil
}

var _ authchallenge.Handler = (*challengeHandler)(nil)
