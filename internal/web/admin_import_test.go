package web

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
)

// /admin's import box (PLAN §24.2 item 3): an OmacDiag report becomes a
// pending report with a driver_missing flag; mistakes come back as messages.
func TestAdminImport(t *testing.T) {
	ts, _, _, _ := adminServer(t, Options{AdminInsecure: true})
	client := ts.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	get := func(path string) string {
		res, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}
	page := get("/admin")
	csrf := regexp.MustCompile(`name="csrf" value="([0-9a-f]+)"`).FindStringSubmatch(page)
	if csrf == nil || !strings.Contains(page, `action="/admin/import"`) {
		t.Fatal("no import form")
	}
	raw, err := os.ReadFile("../results/fixtures/omacdiag-mbp113.json")
	if err != nil {
		t.Fatal(err)
	}
	post := func(fields map[string]string, file []byte) string {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		for k, v := range fields {
			mw.WriteField(k, v)
		}
		if file != nil {
			fw, _ := mw.CreateFormFile("report", "report.json")
			fw.Write(file)
		}
		mw.Close()
		req, _ := http.NewRequest("POST", ts.URL+"/admin/import", &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusSeeOther {
			t.Fatalf("import: %d", res.StatusCode)
		}
		return res.Header.Get("Location")
	}
	if loc := post(map[string]string{"csrf": csrf[1], "format": "omacdiag"}, raw); !strings.Contains(loc, "err=") || !strings.Contains(loc, "Omarchy") {
		t.Errorf("no version: %s", loc)
	}
	if loc := post(map[string]string{"csrf": csrf[1]}, nil); !strings.Contains(loc, "err=") {
		t.Errorf("no file: %s", loc)
	}
	loc := post(map[string]string{"csrf": csrf[1], "format": "omacdiag", "omarchy": "4.0.4", "tester": "carl"}, raw)
	if !strings.HasPrefix(loc, "/admin/report/") {
		t.Fatalf("import: %s", loc)
	}
	report := get(loc)
	for _, want := range []string{"driver_missing", "OmacDiag", "macbookpro11-3-15-late-2013-a"} {
		if !strings.Contains(report, want) {
			t.Errorf("imported report page: missing %q", want)
		}
	}
	if strings.Contains(report, "C02LK1ABFD57") {
		t.Error("the serial number survived")
	}
	// Without the form token, nothing is imported.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("format", "omacdiag")
	mw.Close()
	res, _ := client.Post(ts.URL+"/admin/import", mw.FormDataContentType(), &body)
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("no token: %d", res.StatusCode)
	}
}
