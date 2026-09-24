package group_resource_instance_role_assignments

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestRead(t *testing.T) {
	tests := []struct {
		name      string
		total     int
		role      string
		wantFound bool
		wantPages string
	}{
		// 250 roles is three pages at groupRolesPerPage.
		{"first page", 250, "r5", true, "[1]"},
		{"later page", 250, "r210", true, "[1 2 3]"},
		{"missing", 250, "missing", false, "[1 2 3]"},
		{"no roles", 0, "r0", false, "[1]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var pages []int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				page, err := strconv.Atoi(r.URL.Query().Get("page"))
				if err != nil {
					page = 1 // Same default as the API.
				}
				pages = append(pages, page)

				data := []map[string]any{}
				for i := (page - 1) * groupRolesPerPage; i < page*groupRolesPerPage && i < tt.total; i++ {
					data = append(data, map[string]any{
						"key":               fmt.Sprintf("r%d", i),
						"resource":          map[string]string{"key": "workspace"},
						"resource_instance": map[string]string{"key": "ws-1"},
					})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"data":       data,
					"page_count": (tt.total + groupRolesPerPage - 1) / groupRolesPerPage,
				})
			}))
			defer server.Close()

			client := &groupResourceInstanceRoleAssignmentClient{
				cachedProjectId: "proj",
				cachedEnvId:     "env",
				cachedApiUrl:    server.URL,
				cachedToken:     "token",
			}
			_, err := client.Read(t.Context(), GroupResourceInstanceRoleAssignmentModel{
				Group:            types.StringValue("developers"),
				Role:             types.StringValue(tt.role),
				Resource:         types.StringValue("workspace"),
				ResourceInstance: types.StringValue("ws-1"),
				Tenant:           types.StringValue("default"),
			})

			if found := err == nil; found != tt.wantFound {
				t.Errorf("Read(%q) error = %v, want found %v", tt.role, err, tt.wantFound)
			}
			if got := fmt.Sprint(pages); got != tt.wantPages {
				t.Errorf("Read(%q) requested pages %s, want %s", tt.role, got, tt.wantPages)
			}
		})
	}
}
