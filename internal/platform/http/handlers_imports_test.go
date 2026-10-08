package platformhttp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"panel/internal/db"
	"panel/internal/imports"
	"panel/internal/platform/authz"
	platformstore "panel/internal/platform/store"
	coresites "panel/internal/sites"
	"panel/internal/testpg"
)

func TestCopyUploadEnforcesFileSizeWithoutBuffering(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		limit   int64
		want    string
		wantErr error
	}{
		{name: "below limit", input: "1234", limit: 5, want: "1234"},
		{name: "exact limit", input: "12345", limit: 5, want: "12345"},
		{name: "over limit", input: "123456", limit: 5, want: "12345", wantErr: errImportTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var output bytes.Buffer
			err := copyUpload(&output, strings.NewReader(tt.input), tt.limit)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("copyUpload error = %v, want %v", err, tt.wantErr)
			}
			if output.String() != tt.want {
				t.Fatalf("output = %q, want %q", output.String(), tt.want)
			}
		})
	}
}

type importTestAuth struct{ principal authz.Principal }

func (a importTestAuth) Authenticate(*http.Request) (authz.Principal, bool) {
	return a.principal, a.principal.OrgID != 0
}

type importTestSites map[int32]coresites.Site

func (s importTestSites) ByPublicID(string) (coresites.Site, bool) { return coresites.Site{}, false }
func (s importTestSites) Site(id int32) (coresites.Site, bool)     { site, ok := s[id]; return site, ok }
func (s importTestSites) All() []coresites.Site                    { return nil }

type importTestStore struct{ item imports.Import }

func (s *importTestStore) Create(_ context.Context, input imports.CreateInput) (imports.Import, error) {
	s.item = imports.Import{ID: 9, SiteID: input.SiteID, Source: input.Source, Status: imports.StatusPending, FileName: input.FileName, CreatedAt: time.Now()}
	return s.item, nil
}
func (s *importTestStore) Get(context.Context, int64) (imports.Import, error) { return s.item, nil }
func (s *importTestStore) List(context.Context, int32) ([]imports.Import, error) {
	return []imports.Import{s.item}, nil
}
func (s *importTestStore) Delete(context.Context, int64) (bool, error) { return true, nil }
func (s *importTestStore) SetFailed(context.Context, int64, error) error {
	return nil
}

type importTestQueue struct{ queued bool }

func (q *importTestQueue) Queue(job imports.Job) error {
	q.queued = true
	return os.Remove(job.Path)
}

type importTestExporter struct{}

func (importTestExporter) Write(_ context.Context, output io.Writer, _ int32, _, _ time.Time) error {
	_, err := output.Write([]byte("PK\x03\x04"))
	return err
}

func TestImportAndExportRoutesRequireOwnerOrAdmin(t *testing.T) {
	t.Parallel()
	roles := []struct {
		name string
		role authz.Role
		want int
	}{
		{name: "owner", role: authz.RoleOwner, want: http.StatusAccepted},
		{name: "admin", role: authz.RoleAdmin, want: http.StatusAccepted},
		{name: "editor", role: authz.RoleEditor, want: http.StatusForbidden},
		{name: "viewer", role: authz.RoleViewer, want: http.StatusForbidden},
	}
	for _, tt := range roles {
		t.Run(tt.name, func(t *testing.T) {
			data := &importTestStore{}
			queue := &importTestQueue{}
			handler := New(Deps{
				Authenticator: importTestAuth{principal: authz.Principal{UserID: 1, OrgID: 4, Role: tt.role}},
				Sites:         importTestSites{7: {ID: 7, OrgID: 4, TZ: time.UTC}}, Imports: data, ImportQueue: queue,
				Exporter: importTestExporter{},
			})
			var body bytes.Buffer
			multipartWriter := multipart.NewWriter(&body)
			if err := multipartWriter.WriteField("source", "umami"); err != nil {
				t.Fatal(err)
			}
			file, err := multipartWriter.CreateFormFile("file", "events.csv")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.Write([]byte("created_at,session_id\n")); err != nil {
				t.Fatal(err)
			}
			if err := multipartWriter.Close(); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/private/sites/7/imports", &body)
			request.Header.Set("Content-Type", multipartWriter.FormDataContentType())
			request.Header.Set("X-CSRF-Token", "token")
			request.AddCookie(&http.Cookie{Name: "panel_csrf", Value: "token"})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tt.want {
				t.Fatalf("status = %d body=%s, want %d", response.Code, response.Body.String(), tt.want)
			}
			if tt.want == http.StatusAccepted && !queue.queued {
				t.Fatal("accepted upload was not queued")
			}
		})
	}
}

func TestExportRouteStreamsZIP(t *testing.T) {
	handler := New(Deps{
		Authenticator: importTestAuth{principal: authz.Principal{UserID: 1, OrgID: 4, Role: authz.RoleOwner}},
		Sites:         importTestSites{7: {ID: 7, OrgID: 4, TZ: time.UTC}},
		Exporter:      importTestExporter{},
	})
	request := httptest.NewRequest(http.MethodGet, "/api/private/sites/7/export?from=2026-10-01&to=2026-10-02", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/zip" || response.Body.String() != "PK\x03\x04" {
		t.Fatalf("status=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}
}

func TestImportAndExportMutationsWriteAuditEntries(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		insert into orgs(id,name,slug) overriding system value values (4,'Org','imports-audit');
		insert into users(id,email,name,password_hash) overriding system value values (1,'audit@example.test','Owner','hash');
		insert into memberships(org_id,user_id,role) values (4,1,'owner');
		insert into sites(id,org_id,public_id,name,timezone,retention_class) overriding system value
		values (7,4,'importaudit1','Site','UTC',30)
	`); err != nil {
		t.Fatal(err)
	}
	importStore := imports.NewStore(pool)
	handler := New(Deps{
		Authenticator: importTestAuth{principal: authz.Principal{UserID: 1, OrgID: 4, Role: authz.RoleOwner}},
		Sites:         importTestSites{7: {ID: 7, OrgID: 4, TZ: time.UTC}},
		Store:         &platformstore.Store{Pool: pool},
		Imports:       importStore,
		ImportQueue:   &importTestQueue{},
		Exporter:      importTestExporter{},
	})

	var body bytes.Buffer
	multipartWriter := multipart.NewWriter(&body)
	if err := multipartWriter.WriteField("source", "umami"); err != nil {
		t.Fatal(err)
	}
	file, err := multipartWriter.CreateFormFile("file", "events.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("created_at,session_id\n")); err != nil {
		t.Fatal(err)
	}
	if err := multipartWriter.Close(); err != nil {
		t.Fatal(err)
	}
	upload := httptest.NewRequest(http.MethodPost, "/api/private/sites/7/imports", &body)
	upload.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	withCSRF(upload)
	uploadResponse := httptest.NewRecorder()
	handler.ServeHTTP(uploadResponse, upload)
	if uploadResponse.Code != http.StatusAccepted {
		t.Fatalf("upload status=%d body=%s", uploadResponse.Code, uploadResponse.Body.String())
	}

	items, err := importStore.List(ctx, 7)
	if err != nil || len(items) != 1 {
		t.Fatalf("imports=%v err=%v", items, err)
	}
	remove := httptest.NewRequest(http.MethodDelete, "/api/private/imports/"+fmt.Sprint(items[0].ID), nil)
	withCSRF(remove)
	removeResponse := httptest.NewRecorder()
	handler.ServeHTTP(removeResponse, remove)
	if removeResponse.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", removeResponse.Code, removeResponse.Body.String())
	}
	export := httptest.NewRequest(http.MethodGet, "/api/private/sites/7/export?from=2026-10-01&to=2026-10-02", nil)
	exportResponse := httptest.NewRecorder()
	handler.ServeHTTP(exportResponse, export)
	if exportResponse.Code != http.StatusOK {
		t.Fatalf("export status=%d", exportResponse.Code)
	}

	rows, err := pool.Query(ctx, `select action from audit_log order by id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, action)
	}
	want := []string{"import.created", "import.deleted", "site.exported"}
	if !slices.Equal(actions, want) {
		t.Fatalf("audit actions=%v, want %v", actions, want)
	}
}

func withCSRF(request *http.Request) {
	request.Header.Set("X-CSRF-Token", "token")
	request.AddCookie(&http.Cookie{Name: "panel_csrf", Value: "token"})
}

func TestSaveImportMultipartStreamsFileAndAcceptsSourceAfterIt(t *testing.T) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	file, err := w.CreateFormFile("file", "../umami.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("created_at,session_id\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteField("source", "umami"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/", &body)
	request.Header.Set("Content-Type", w.FormDataContentType())

	upload, err := saveImportMultipart(request, t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(upload.Path)
	if upload.Source != "umami" || upload.FileName != "umami.csv" || filepath.Dir(upload.Path) == "." {
		t.Fatalf("upload = %+v", upload)
	}
	contents, err := os.ReadFile(upload.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "created_at,session_id\n" {
		t.Fatalf("contents = %q", contents)
	}
}
