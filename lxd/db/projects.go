//go:build linux && cgo && !agent

package db

import (
	"database/sql"
	"fmt"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t projects.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -p db -e project objects
//go:generate mapper stmt -p db -e project objects-by-Name
//go:generate mapper stmt -p db -e project objects-by-ID
//go:generate mapper stmt -p db -e project create struct=Project
//go:generate mapper stmt -p db -e project id
//go:generate mapper stmt -p db -e project rename
//go:generate mapper stmt -p db -e project update struct=Project
//go:generate mapper stmt -p db -e project delete-by-Name
//
//go:generate mapper method -p db -e project GetMany references=Config version=2
//go:generate mapper method -p db -e project GetOne struct=Project version=2
//go:generate mapper method -p db -e project Exists struct=Project version=2
//go:generate mapper method -p db -e project Create references=Config version=2
//go:generate mapper method -p db -e project ID struct=Project version=2
//go:generate mapper method -p db -e project Rename version=2
//go:generate mapper method -p db -e project DeleteOne-by-Name version=2

// Project represents a LXD project
type Project struct {
	ID          int
	Description string
	Name        string `db:"omit=update"`
}

// ProjectFilter specifies potential query parameter fields.
type ProjectFilter struct {
	ID   *int
	Name *string `db:"omit=update"` // If non-empty, return only the project with this name.
}

// ToAPI converts the database Project struct to an api.Project entry.
func (p *Project) ToAPI(tx *ClusterTx) (*api.Project, error) {
	apiProject := &api.Project{
		ProjectPut: api.ProjectPut{
			Description: p.Description,
		},
		Name: p.Name,
	}

	var err error
	apiProject.Config, err = tx.GetProjectConfig(p.ID)
	if err != nil {
		return nil, err
	}

	return apiProject, nil
}

// ProjectHasProfiles is a helper to check if a project has the profiles
// feature enabled.
func (c *ClusterTx) ProjectHasProfiles(name string) (bool, error) {
	return projectHasProfiles(c.tx, name)
}

// GetProjectNames returns the names of all available projects.
func (c *ClusterTx) GetProjectNames() ([]string, error) {
	stmt := "SELECT name FROM projects"

	names, err := query.SelectStrings(c.tx, stmt)
	if err != nil {
		return nil, fmt.Errorf("Fetch project names: %w", err)
	}

	return names, nil
}

// GetProjectIDsToNames returns a map associating each project ID to its
// project name.
func (c *ClusterTx) GetProjectIDsToNames() (map[int64]string, error) {
	stmt := "SELECT id, name FROM projects"

	rows, err := c.tx.Query(stmt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := map[int64]string{}
	for i := 0; rows.Next(); i++ {
		var id int64
		var name string

		err := rows.Scan(&id, &name)
		if err != nil {
			return nil, err
		}

		result[id] = name
	}

	err = rows.Err()
	if err != nil {
		return nil, err
	}

	return result, nil
}

func projectHasProfiles(tx *sql.Tx, name string) (bool, error) {
	stmt := `
SELECT projects_config.value
  FROM projects_config
  JOIN projects ON projects.id=projects_config.project_id
 WHERE projects.name=? AND projects_config.key='features.profiles'
`
	values, err := query.SelectStrings(tx, stmt, name)
	if err != nil {
		return false, fmt.Errorf("Fetch project config: %w", err)
	}

	if len(values) == 0 {
		return false, nil
	}

	return shared.IsTrue(values[0]), nil
}

// ProjectHasImages is a helper to check if a project has the images
// feature enabled.
func (c *ClusterTx) ProjectHasImages(name string) (bool, error) {
	project, err := c.GetProject(name)
	if err != nil {
		return false, fmt.Errorf("fetch project: %w", err)
	}

	config, err := c.GetProjectConfig(project.ID)
	if err != nil {
		return false, err
	}

	enabled := shared.IsTrue(config["features.images"])

	return enabled, nil
}

// UpdateProject updates the project matching the given key parameters.
func (c *ClusterTx) UpdateProject(name string, object api.ProjectPut) error {
	id, err := c.GetProjectID(name)
	if err != nil {
		return fmt.Errorf("Fetch project ID: %w", err)
	}

	stmt := c.stmt(projectUpdate)
	result, err := stmt.Exec(object.Description, id)
	if err != nil {
		return fmt.Errorf("Update project: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("Fetch affected rows: %w", err)
	}
	if n != 1 {
		return fmt.Errorf("Query updated %d rows instead of 1", n)
	}

	// Clear config.
	_, err = c.tx.Exec(`
DELETE FROM projects_config WHERE projects_config.project_id = ?
`, id)
	if err != nil {
		return fmt.Errorf("Delete project config: %w", err)
	}

	err = c.UpdateConfig("project", int(id), object.Config)
	if err != nil {
		return fmt.Errorf("Insert config for project: %w", err)
	}

	return nil
}

// InitProjectWithoutImages updates populates the images_profiles table with
// all images from the default project when a project is created with
// features.images=false.
func (c *ClusterTx) InitProjectWithoutImages(project string) error {
	defaultProfileID, err := c.GetProfileID(project, "default")
	if err != nil {
		return fmt.Errorf("Fetch project ID: %w", err)
	}
	stmt := `INSERT INTO images_profiles (image_id, profile_id)
	SELECT images.id, ? FROM images WHERE project_id=1`
	_, err = c.tx.Exec(stmt, defaultProfileID)
	return err
}

// GetProject returns the project with the given key.
func (c *Cluster) GetProject(projectName string) (*Project, error) {
	var err error
	var p *Project
	err = c.Transaction(func(tx *ClusterTx) error {
		p, err = tx.GetProject(projectName)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return p, nil
}
