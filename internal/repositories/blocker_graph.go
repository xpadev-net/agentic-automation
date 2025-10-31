package repositories

import (
	"errors"
	"strings"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"

	"agentic-automation/internal/config"
	"agentic-automation/internal/models"
)

// BlockerGraphRepository provides data access methods for blocker graph edges
type BlockerGraphRepository struct {
	db *gorm.DB
}

// NewBlockerGraphRepository creates a new BlockerGraphRepository instance
func NewBlockerGraphRepository() *BlockerGraphRepository {
	return &BlockerGraphRepository{
		db: config.GetDB(),
	}
}

// isDuplicateKeyError checks if the error is a MySQL duplicate key violation
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		// MySQL error code 1062 is "Duplicate entry"
		return mysqlErr.Number == 1062
	}
	
	// Also check error message as fallback
	errMsg := strings.ToLower(err.Error())
	return strings.Contains(errMsg, "duplicate entry") || 
		   strings.Contains(errMsg, "duplicate key")
}

// CreateEdge creates a single dependency edge
// This operation is idempotent - duplicate edges are silently ignored
func (r *BlockerGraphRepository) CreateEdge(taskID, dependsOnTaskID int) error {
	edge := models.BlockerGraphEdge{
		TaskID:          taskID,
		DependsOnTaskID: dependsOnTaskID,
	}

	result := r.db.Create(&edge)
	if result.Error != nil {
		// Check if error is due to duplicate key (idempotent behavior)
		if isDuplicateKeyError(result.Error) {
			return nil // Silently ignore duplicate edges
		}
		return result.Error
	}

	return nil
}

// CreateEdges creates multiple dependency edges in batch
// Duplicate edges are silently ignored (idempotent operation)
func (r *BlockerGraphRepository) CreateEdges(edges []models.BlockerGraphEdge) error {
	if len(edges) == 0 {
		return nil
	}

	// Create edges one by one, ignoring duplicates
	// CreateEdge already handles duplicate key errors gracefully
	for _, edge := range edges {
		if err := r.CreateEdge(edge.TaskID, edge.DependsOnTaskID); err != nil {
			// CreateEdge already handles duplicate errors, so any error here is unexpected
			return err
		}
	}

	return nil
}

// DeleteEdge deletes a specific dependency edge
func (r *BlockerGraphRepository) DeleteEdge(taskID, dependsOnTaskID int) error {
	result := r.db.Where("task_id = ? AND depends_on_task_id = ?", taskID, dependsOnTaskID).
		Delete(&models.BlockerGraphEdge{})
	
	if result.Error != nil {
		return result.Error
	}

	return nil
}

// GetDependenciesForTask returns all dependencies for a given task
// Returns edges where task_id = taskID (tasks that this task depends on)
// Returns empty slice (not nil) if no dependencies found
func (r *BlockerGraphRepository) GetDependenciesForTask(taskID int) ([]models.BlockerGraphEdge, error) {
	var edges []models.BlockerGraphEdge

	result := r.db.Where("task_id = ?", taskID).Find(&edges)
	if result.Error != nil {
		return nil, result.Error
	}

	// Return empty slice instead of nil
	if edges == nil {
		edges = []models.BlockerGraphEdge{}
	}

	return edges, nil
}

// FindTasksBlockedBy returns all tasks that are blocked by a specific issue
// Returns edges where depends_on_task_id = issueID (tasks waiting for this issue to complete)
// Returns empty slice (not nil) if no tasks found
func (r *BlockerGraphRepository) FindTasksBlockedBy(issueID int) ([]models.BlockerGraphEdge, error) {
	var edges []models.BlockerGraphEdge

	result := r.db.Where("depends_on_task_id = ?", issueID).Find(&edges)
	if result.Error != nil {
		return nil, result.Error
	}

	// Return empty slice instead of nil
	if edges == nil {
		edges = []models.BlockerGraphEdge{}
	}

	return edges, nil
}

// GetAllEdges returns all edges in the blocker graph
// Used for cycle detection and full graph traversal
// Returns empty slice (not nil) if no edges found
func (r *BlockerGraphRepository) GetAllEdges() ([]models.BlockerGraphEdge, error) {
	var edges []models.BlockerGraphEdge

	result := r.db.Find(&edges)
	if result.Error != nil {
		return nil, result.Error
	}

	// Return empty slice instead of nil
	if edges == nil {
		edges = []models.BlockerGraphEdge{}
	}

	return edges, nil
}

// DeleteEdgesByTaskID deletes all edges where the task is involved as either task_id or depends_on_task_id
// Used for cleanup when an issue is deleted or dependencies need to be cleared
func (r *BlockerGraphRepository) DeleteEdgesByTaskID(taskID int) error {
	result := r.db.Where("task_id = ? OR depends_on_task_id = ?", taskID, taskID).
		Delete(&models.BlockerGraphEdge{})
	
	if result.Error != nil {
		return result.Error
	}

	return nil
}

