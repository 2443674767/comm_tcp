package chat

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestVoiceSupportDiagnosticsAuthenticatedAndBounded(t *testing.T) {
	ts, _ := testServer(t)
	payload := `{"stage":"before-join","build":"voice-diagnostic-1","secure":true,"rtc_global":"undefined","rtc_window":"undefined","rtc_webkit":"function","rtc_moz":"undefined","transceiver":false,"microphone":true,"audio_state":"running"}`
	resp := request(t, ts, "POST", "/api/voice-support", payload, nil)
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("anonymous diagnostic: %d", resp.StatusCode)
	}
	cookie := login(t, ts, "诊断用户")
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	resp = request(t, ts, "POST", "/api/voice-support", payload, cookie)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("diagnostic: %d", resp.StatusCode)
	}
	text := output.String()
	if !strings.Contains(text, "before-join") || !strings.Contains(text, `"rtc_webkit":"function"`) {
		t.Fatalf("missing capabilities: %s", text)
	}
	if strings.Contains(text, cookie.Value) || strings.Contains(text, "诊断用户") {
		t.Fatal("diagnostic exposed session identity")
	}
	for _, body := range []string{strings.Replace(payload, "before-join", "arbitrary", 1), strings.Replace(payload, `"rtc_global":"undefined"`, `"rtc_global":"`+strings.Repeat("x", 200)+`"`, 1), strings.TrimSuffix(payload, "}") + `,"token":"secret"}`} {
		resp = request(t, ts, "POST", "/api/voice-support", body, cookie)
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Fatalf("invalid diagnostic accepted: %d", resp.StatusCode)
		}
	}
}
