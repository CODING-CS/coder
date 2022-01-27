package codersdk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/coder/coder/coderd"
)

// Projects lists projects inside an organization.
// If organization is an empty string, all projects will be returned
// for the authenticated user.
func (c *Client) Projects(ctx context.Context, organization string) ([]coderd.Project, error) {
	route := "/api/v2/projects"
	if organization != "" {
		route = fmt.Sprintf("/api/v2/projects/%s", organization)
	}
	res, err := c.request(ctx, http.MethodGet, route, nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, readBodyAsError(res)
	}
	var projects []coderd.Project
	return projects, json.NewDecoder(res.Body).Decode(&projects)
}

// Project returns a single project.
func (c *Client) Project(ctx context.Context, organization, project string) (coderd.Project, error) {
	res, err := c.request(ctx, http.MethodGet, fmt.Sprintf("/api/v2/projects/%s/%s", organization, project), nil)
	if err != nil {
		return coderd.Project{}, nil
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return coderd.Project{}, readBodyAsError(res)
	}
	var resp coderd.Project
	return resp, json.NewDecoder(res.Body).Decode(&resp)
}

// CreateProject creates a new project inside an organization.
func (c *Client) CreateProject(ctx context.Context, organization string, request coderd.CreateProjectRequest) (coderd.Project, error) {
	res, err := c.request(ctx, http.MethodPost, fmt.Sprintf("/api/v2/projects/%s", organization), request)
	if err != nil {
		return coderd.Project{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		return coderd.Project{}, readBodyAsError(res)
	}
	var project coderd.Project
	return project, json.NewDecoder(res.Body).Decode(&project)
}

// ProjectHistory lists history for a project.
func (c *Client) ProjectHistory(ctx context.Context, organization, project string) ([]coderd.ProjectHistory, error) {
	res, err := c.request(ctx, http.MethodGet, fmt.Sprintf("/api/v2/projects/%s/%s/history", organization, project), nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, readBodyAsError(res)
	}
	var projectVersions []coderd.ProjectHistory
	return projectVersions, json.NewDecoder(res.Body).Decode(&projectVersions)
}

// CreateProjectHistory inserts a new version for the project.
func (c *Client) CreateProjectHistory(ctx context.Context, organization, project string, request coderd.CreateProjectVersionRequest) (coderd.ProjectHistory, error) {
	res, err := c.request(ctx, http.MethodPost, fmt.Sprintf("/api/v2/projects/%s/%s/history", organization, project), request)
	if err != nil {
		return coderd.ProjectHistory{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		return coderd.ProjectHistory{}, readBodyAsError(res)
	}
	var projectVersion coderd.ProjectHistory
	return projectVersion, json.NewDecoder(res.Body).Decode(&projectVersion)
}