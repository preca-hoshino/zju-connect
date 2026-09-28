package easyconnect

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mythologyli/zju-connect/client/authchallenge"
	"github.com/mythologyli/zju-connect/underlay"
	"github.com/pquerna/otp/totp"
)

// shuStyleTOTPResponse reproduces the reply of a Sangfor build (M7.6.8R2 at
// vpn2.shu.edu.cn) that reports an accepted token via the regular <Auth>
// envelope instead of the "Totp auth succ" marker ZJU-style builds emit.
const shuStyleTOTPResponse = `<?xml version="1.0" encoding="utf-8"?>
<Auth>
	<LBEnabled>1</LBEnabled>
	<TwfID>af80770851afe9dd</TwfID>
	<pwpErrorCode>0</pwpErrorCode>
	<ErrorMsg>Successful</ErrorMsg>
	<ErrorCode>1</ErrorCode>
	<Result>1</Result>
	<CurAuth>7</CurAuth>
	<EnableMAM>0</EnableMAM>
	<CSRF_RAND_CODE>2000631544</CSRF_RAND_CODE>
	<AuthInfo><![CDATA[]]></AuthInfo>
	<IsFirstAuth>0</IsFirstAuth>
</Auth>`

// fixedCodeHandler returns a challenge handler that always answers with code.
func fixedCodeHandler(code string) authchallenge.Handler {
	return authchallenge.HandlerFuncs{
		Code: func(authchallenge.CodeChallenge) (authchallenge.CodeResponse, error) {
			return authchallenge.CodeResponse{Code: code}, nil
		},
	}
}

func newTOTPTestClient(t *testing.T, response string) *Client {
	t.Helper()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/por/login_token.csp" {
			t.Errorf("request path = %q, want /por/login_token.csp", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		// loginTOTP sends a bare urlencoded body without a Content-Type header,
		// so parse it explicitly instead of relying on r.ParseForm.
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Errorf("parse body %q: %v", body, err)
		}
		if got := form.Get("svpn_inputtoken"); got != "123456" {
			t.Errorf("svpn_inputtoken = %q, want %q", got, "123456")
		}
		if got := r.Header.Get("Cookie"); got != "TWFID=initial-twfid" {
			t.Errorf("Cookie = %q, want %q", got, "TWFID=initial-twfid")
		}
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(server.Close)

	client := NewClient(Options{
		Server:           strings.TrimPrefix(server.URL, "https://"),
		SessionID:        "initial-twfid",
		ChallengeHandler: fixedCodeHandler("123456"),
	})
	client.httpClient = server.Client()
	t.Cleanup(client.Close)

	return client
}

func TestLoginTOTPAcceptsAuthEnvelopeResponse(t *testing.T) {
	client := newTOTPTestClient(t, shuStyleTOTPResponse)

	if err := client.loginTOTP(); err != nil {
		t.Fatalf("loginTOTP error = %v, want nil", err)
	}
	if client.twfID != "af80770851afe9dd" {
		t.Fatalf("twfID = %q, want %q", client.twfID, "af80770851afe9dd")
	}
}

func TestLoginTOTPAcceptsLegacySuccessMarker(t *testing.T) {
	client := newTOTPTestClient(t, `<Auth><TwfID>legacy-twfid</TwfID>Totp auth succ</Auth>`)

	if err := client.loginTOTP(); err != nil {
		t.Fatalf("loginTOTP error = %v, want nil", err)
	}
	if client.twfID != "legacy-twfid" {
		t.Fatalf("twfID = %q, want %q", client.twfID, "legacy-twfid")
	}
}

func TestLoginTOTPRejectsFailedResponse(t *testing.T) {
	response := strings.Replace(shuStyleTOTPResponse, "<pwpErrorCode>0</pwpErrorCode>", "<pwpErrorCode>1</pwpErrorCode>", 1)
	response = strings.Replace(response, "<Result>1</Result>", "<Result>0</Result>", 1)
	client := newTOTPTestClient(t, response)

	err := client.loginTOTP()
	if err == nil {
		t.Fatal("loginTOTP unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "TOTP verification failed") {
		t.Fatalf("loginTOTP error = %v, want verification failure", err)
	}
	if client.twfID != "initial-twfid" {
		t.Fatalf("twfID changed to %q on failure", client.twfID)
	}
}

func TestLoginTOTPKeepsTwfIDWhenResponseHasNone(t *testing.T) {
	client := newTOTPTestClient(t, `<Auth><Result>1</Result><ErrorMsg>Successful</ErrorMsg></Auth>`)

	if err := client.loginTOTP(); err != nil {
		t.Fatalf("loginTOTP error = %v, want nil", err)
	}
	if client.twfID != "initial-twfid" {
		t.Fatalf("twfID = %q, want %q", client.twfID, "initial-twfid")
	}
}

func TestLoginTOTPGeneratesCodeFromSecret(t *testing.T) {
	secret := "JBSWY3DPEHPK3PXP"
	expected, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate expected code: %v", err)
	}

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Errorf("parse body %q: %v", body, err)
		}
		if got := form.Get("svpn_inputtoken"); got != expected {
			t.Errorf("svpn_inputtoken = %q, want generated code %q", got, expected)
		}
		_, _ = w.Write([]byte(shuStyleTOTPResponse))
	}))
	defer server.Close()

	client := NewClient(Options{
		Server:    strings.TrimPrefix(server.URL, "https://"),
		SessionID: "initial-twfid",
		Auth:      AuthOptions{TOTPSecret: secret},
	})
	client.httpClient = server.Client()
	defer client.Close()

	if err := client.loginTOTP(); err != nil {
		t.Fatalf("loginTOTP error = %v, want nil", err)
	}
}

func TestLoginTOTPReturnsChallengeHandlerError(t *testing.T) {
	dialer := newTestUnderlay(t, underlay.Options{AutoDetect: false})
	handlerErr := errTOTPRequired
	client := NewClient(Options{
		Server:         "vpn.example.com:443",
		SessionID:      "initial-twfid",
		UnderlayDialer: dialer,
		ChallengeHandler: authchallenge.HandlerFuncs{
			Code: func(authchallenge.CodeChallenge) (authchallenge.CodeResponse, error) {
				return authchallenge.CodeResponse{}, handlerErr
			},
		},
	})
	defer client.Close()

	if err := client.loginTOTP(); !errors.Is(err, handlerErr) {
		t.Fatalf("loginTOTP error = %v, want %v", err, handlerErr)
	}
}
