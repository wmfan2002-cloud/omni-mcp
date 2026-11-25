package app_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
)

func TestAgentsPortalLoginApproveAndRevoke(t *testing.T) {
	h := newHarness(t)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// 1. Agent starts pairing without needing VPS SSH
	startBody := []byte(`{"name":"mypc","os":"windows"}`)
	resp, err := http.Post(h.base+"/agent/pair/start", "application/json", bytes.NewReader(startBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pair/start status=%d", resp.StatusCode)
	}
	var startRes struct {
		Code       string `json:"code"`
		PollToken  string `json:"poll_token"`
		ApproveURL string `json:"approve_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&startRes); err != nil {
		t.Fatal(err)
	}

	// 2. Unauthenticated GET /agents?code=... renders login page showing verification code
	pageResp, err := client.Get(h.base + "/agents?code=" + startRes.Code)
	if err != nil {
		t.Fatal(err)
	}
	loginHTML, _ := io.ReadAll(pageResp.Body)
	_ = pageResp.Body.Close()
	if pageResp.StatusCode != http.StatusOK || !strings.Contains(string(loginHTML), startRes.Code) {
		t.Fatalf("expected login page with code %s, got status=%d", startRes.Code, pageResp.StatusCode)
	}

	// 3. Login with Hub admin credentials
	form := url.Values{
		"username": {"tester"},
		"password": {testPassword},
		"next":     {"/agents?code=" + startRes.Code},
		"code":     {startRes.Code},
	}
	loginResp, err := client.PostForm(h.base+"/agents/login", form)
	if err != nil {
		t.Fatal(err)
	}
	_ = loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 after login, got %d", loginResp.StatusCode)
	}

	// 4. Authenticated GET /agents sets CSRF cookie and renders management portal
	authedResp, err := client.Get(h.base + "/agents?code=" + startRes.Code)
	if err != nil {
		t.Fatal(err)
	}
	portalBody, _ := io.ReadAll(authedResp.Body)
	_ = authedResp.Body.Close()
	if authedResp.StatusCode != http.StatusOK || !strings.Contains(string(portalBody), "设备管理中心") {
		t.Fatalf("expected authenticated portal page, got status=%d", authedResp.StatusCode)
	}
	u, _ := url.Parse(h.base + "/agents")
	csrf := ""
	for _, c := range jar.Cookies(u) {
		if c.Name == "omni_portal_csrf" {
			csrf = c.Value
		}
	}
	if csrf == "" {
		t.Fatal("expected omni_portal_csrf cookie")
	}

	// 5. Approve without CSRF must be rejected
	badReq, _ := http.NewRequest(http.MethodPost, h.base+"/agents/api/approve", strings.NewReader(`{"code":"`+startRes.Code+`"}`))
	badReq.Header.Set("Content-Type", "application/json")
	badResp, err := client.Do(badReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = badResp.Body.Close()
	if badResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 without CSRF, got %d", badResp.StatusCode)
	}

	// 6. Approve with CSRF succeeds
	okReq, _ := http.NewRequest(http.MethodPost, h.base+"/agents/api/approve", strings.NewReader(`{"code":"`+startRes.Code+`"}`))
	okReq.Header.Set("Content-Type", "application/json")
	okReq.Header.Set("X-Omni-CSRF", csrf)
	okResp, err := client.Do(okReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = okResp.Body.Close()
	if okResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on approve, got %d", okResp.StatusCode)
	}

	// 7. Agent polls and receives its issued per-device token
	pollBody, _ := json.Marshal(map[string]string{"code": startRes.Code, "poll_token": startRes.PollToken})
	pollResp, err := http.Post(h.base+"/agent/pair/poll", "application/json", bytes.NewReader(pollBody))
	if err != nil {
		t.Fatal(err)
	}
	var pollRes struct {
		Status string `json:"status"`
		Name   string `json:"name"`
		Token  string `json:"token"`
	}
	_ = json.NewDecoder(pollResp.Body).Decode(&pollRes)
	_ = pollResp.Body.Close()
	if pollRes.Status != "approved" || pollRes.Name != "mypc" || pollRes.Token == "" {
		t.Fatalf("unexpected poll result: %+v", pollRes)
	}

	// 8. Revoke device from portal
	revReq, _ := http.NewRequest(http.MethodPost, h.base+"/agents/api/revoke", strings.NewReader(`{"name":"mypc"}`))
	revReq.Header.Set("Content-Type", "application/json")
	revReq.Header.Set("X-Omni-CSRF", csrf)
	revResp, err := client.Do(revReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = revResp.Body.Close()
	if revResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on revoke, got %d", revResp.StatusCode)
	}
}
