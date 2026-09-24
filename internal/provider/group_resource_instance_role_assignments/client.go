package group_resource_instance_role_assignments

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/permitio/permit-golang/pkg/permit"
)

type groupResourceInstanceRoleAssignmentClient struct {
	client *permit.Client
	// Cache context info after first retrieval
	cachedProjectId string
	cachedEnvId     string
	cachedApiUrl    string
	cachedToken     string
}

// getContextInfo extracts project and environment IDs from a tenant API call.
func (c *groupResourceInstanceRoleAssignmentClient) getContextInfo(ctx context.Context) (projectId, envId, apiUrl, token string, err error) {
	// Return cached values if available
	if c.cachedProjectId != "" && c.cachedEnvId != "" {
		return c.cachedProjectId, c.cachedEnvId, c.cachedApiUrl, c.cachedToken, nil
	}

	// Call Tenants.List() to get at least one tenant which contains project_id and environment_id
	tenants, err := c.client.Api.Tenants.List(ctx, 1, 1)
	if err != nil {
		err = fmt.Errorf("failed to get context info from tenants API: %w", err)
		return
	}

	if len(tenants) == 0 {
		err = fmt.Errorf("no tenants found - cannot determine project_id and environment_id. Make sure at least one tenant exists in your Permit environment")
		return
	}

	// Extract project_id and environment_id from the first tenant
	firstTenant := tenants[0]
	projectId = firstTenant.ProjectId
	envId = firstTenant.EnvironmentId

	// Use cached token and API URL if available (set during Configure)
	if c.cachedToken != "" {
		token = c.cachedToken
		apiUrl = c.cachedApiUrl
		if apiUrl == "" {
			apiUrl = "https://api.permit.io"
		}
	} else {
		// Fallback: try environment variables
		token = getTokenFromEnv()
		apiUrl = "https://api.permit.io"
		if token == "" {
			err = fmt.Errorf("API token not found - ensure provider is configured with api_key")
			return
		}
	}

	// Cache the values
	c.cachedProjectId = projectId
	c.cachedEnvId = envId
	c.cachedApiUrl = apiUrl
	c.cachedToken = token

	return
}

// getTokenFromEnv gets the API token from environment variables.
func getTokenFromEnv() string {
	// Check the same environment variable the provider uses
	if token := getEnv("PERMITIO_API_KEY"); token != "" {
		return token
	}
	if token := getEnv("PERMIT_API_KEY"); token != "" {
		return token
	}
	return ""
}

func getEnv(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}

func (c *groupResourceInstanceRoleAssignmentClient) Create(ctx context.Context, plan *GroupResourceInstanceRoleAssignmentModel) error {
	projectId, envId, apiUrl, token, err := c.getContextInfo(ctx)
	if err != nil {
		return err
	}

	httpClient := http.DefaultClient

	apiUrl = strings.TrimSuffix(apiUrl, "/")
	url := fmt.Sprintf("%s/v2/schema/%s/%s/groups/%s/roles", apiUrl, projectId, envId, plan.Group.ValueString())

	// Prepare request body
	body := GroupAddRole{
		Role:             plan.Role.ValueString(),
		Resource:         plan.Resource.ValueString(),
		ResourceInstance: plan.ResourceInstance.ValueString(),
		Tenant:           plan.Tenant.ValueString(),
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(bodyJSON))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Content-Type", "application/json")

	// Execute request
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	// Generate ID for Terraform state
	plan.Id = plan.Group
	return nil
}

func (c *groupResourceInstanceRoleAssignmentClient) Read(ctx context.Context, data GroupResourceInstanceRoleAssignmentModel) (GroupResourceInstanceRoleAssignmentModel, error) {
	projectId, envId, apiUrl, token, err := c.getContextInfo(ctx)
	if err != nil {
		return GroupResourceInstanceRoleAssignmentModel{}, err
	}

	apiUrl = strings.TrimSuffix(apiUrl, "/")
	url := fmt.Sprintf("%s/v2/schema/%s/%s/groups/%s/roles", apiUrl, projectId, envId, data.Group.ValueString())

	// The list is paginated, so check every page before treating the assignment as gone
	for page := 1; ; page++ {
		result, err := listGroupRolesPage(ctx, url, token, page)
		if err != nil {
			return GroupResourceInstanceRoleAssignmentModel{}, err
		}

		// Find the matching role assignment
		for _, item := range result.Data {
			if item.Key == data.Role.ValueString() &&
				item.Resource.Key == data.Resource.ValueString() &&
				item.ResourceInstance.Key == data.ResourceInstance.ValueString() {
				return data, nil
			}
		}

		if page >= result.PageCount {
			break
		}
	}

	return GroupResourceInstanceRoleAssignmentModel{}, fmt.Errorf("group resource instance role assignment not found")
}

// groupRolesPerPage is the largest page size the group roles endpoint accepts.
const groupRolesPerPage = 100

type groupRolesPage struct {
	Data []struct {
		Key              string `json:"key"`
		ResourceInstance struct {
			Key string `json:"key"`
		} `json:"resource_instance"`
		Resource struct {
			Key string `json:"key"`
		} `json:"resource"`
	} `json:"data"`
	PageCount int `json:"page_count"`
}

func listGroupRolesPage(ctx context.Context, url, token string, page int) (groupRolesPage, error) {
	httpClient := http.DefaultClient

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s?page=%d&per_page=%d", url, page, groupRolesPerPage), nil)
	if err != nil {
		return groupRolesPage{}, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Content-Type", "application/json")

	// Execute request
	resp, err := httpClient.Do(req)
	if err != nil {
		return groupRolesPage{}, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return groupRolesPage{}, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return groupRolesPage{}, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var result groupRolesPage
	if err := json.Unmarshal(respBody, &result); err != nil {
		return groupRolesPage{}, fmt.Errorf("failed to parse response: %w", err)
	}

	return result, nil
}

func (c *groupResourceInstanceRoleAssignmentClient) Delete(ctx context.Context, plan *GroupResourceInstanceRoleAssignmentModel) error {
	projectId, envId, apiUrl, token, err := c.getContextInfo(ctx)
	if err != nil {
		return err
	}

	httpClient := http.DefaultClient

	apiUrl = strings.TrimSuffix(apiUrl, "/")
	url := fmt.Sprintf("%s/v2/schema/%s/%s/groups/%s/roles", apiUrl, projectId, envId, plan.Group.ValueString())

	// Prepare request body
	body := GroupAddRole{
		Role:             plan.Role.ValueString(),
		Resource:         plan.Resource.ValueString(),
		ResourceInstance: plan.ResourceInstance.ValueString(),
		Tenant:           plan.Tenant.ValueString(),
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "DELETE", url, bytes.NewBuffer(bodyJSON))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Content-Type", "application/json")

	// Execute request
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body for error details
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}
