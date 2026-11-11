package changes

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var ErrConflict = errors.New("comparison state changed; reload before applying this action")

func Storage() (string, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "go-review", "comparisons"), nil
}

func atomicJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".pending-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// Publication never uses ordinary latest-review storage or changes Application.
func Publish(storage string, c *Comparison) error {
	if c.ID == "" {
		return fmt.Errorf("comparison must identify its owned storage directory")
	}
	if err := ownedDirectory(storage, c.Directory); err != nil {
		return err
	}
	if err := atomicJSON(filepath.Join(c.Directory, "comparison.json"), c); err != nil {
		return err
	}
	return atomicJSON(filepath.Join(storage, "current", identity(c.Revisions.Repository)+".json"), struct {
		Directory string `json:"directory"`
	}{c.Directory})
}

func OpenCurrent(storage, repository string) (*Comparison, error) {
	data, err := os.ReadFile(filepath.Join(storage, "current", identity(repository)+".json"))
	if err != nil {
		return nil, err
	}
	var pointer struct {
		Directory string `json:"directory"`
	}
	if err := json.Unmarshal(data, &pointer); err != nil {
		return nil, err
	}
	if err := ownedDirectory(storage, pointer.Directory); err != nil {
		return nil, err
	}
	data, err = os.ReadFile(filepath.Join(pointer.Directory, "comparison.json"))
	if err != nil {
		return nil, err
	}
	var c Comparison
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.Directory != pointer.Directory || c.Revisions.Repository != repository || len(c.ID) != 64 || !commitOID.MatchString(c.ID) {
		return nil, fmt.Errorf("comparison identity mismatch")
	}
	return &c, nil
}

type State struct {
	ComparisonID string            `json:"comparisonID"`
	Version      uint64            `json:"version"`
	Items        map[string]string `json:"items"`
	Flows        map[string]bool   `json:"flows"`
}

type StateAction struct {
	ComparisonID string   `json:"comparisonID"`
	Version      uint64   `json:"version"`
	ItemIDs      []string `json:"itemIDs,omitempty"`
	Status       string   `json:"status,omitempty"`
	FlowID       string   `json:"flowID,omitempty"`
	Considered   *bool    `json:"considered,omitempty"`
}

func (c *Comparison) statePath() string {
	return filepath.Join(filepath.Dir(c.Directory), "state", c.ID+".json")
}

func (c *Comparison) State() (State, error) {
	state := State{ComparisonID: c.ID, Items: map[string]string{}, Flows: map[string]bool{}}
	data, err := os.ReadFile(c.statePath())
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	err = json.Unmarshal(data, &state)
	if err == nil && (state.ComparisonID != c.ID || state.Items == nil || state.Flows == nil) {
		err = fmt.Errorf("invalid evidence-bound review state")
	}
	return state, err
}

func (c *Comparison) UpdateState(action StateAction) (state State, err error) {
	if err := c.ValidateStateAction(action); err != nil {
		return State{}, err
	}
	path := c.statePath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return State{}, err
	}
	release, err := acquireStateLock(path + ".lock")
	if err != nil {
		return State{}, err
	}
	defer func() { err = errors.Join(err, release()) }()
	state, err = c.State()
	if err != nil {
		return state, err
	}
	if state.Version != action.Version {
		return state, ErrConflict
	}
	for _, id := range action.ItemIDs {
		state.Items[id] = action.Status
	}
	if action.FlowID != "" {
		state.Flows[action.FlowID] = *action.Considered
	}
	state.Version++
	if err := atomicJSON(path, state); err != nil {
		return State{}, err
	}
	return state, nil
}

func (c *Comparison) ValidateStateAction(action StateAction) error {
	if action.ComparisonID != c.ID {
		return fmt.Errorf("stale comparison identity")
	}
	if action.FlowID == "" {
		switch action.Status {
		case "not-reviewed", "reviewed", "follow-up":
		default:
			return fmt.Errorf("unknown local review state")
		}
	}
	known := map[string]bool{}
	for _, file := range c.Files {
		for _, item := range file.Items {
			known[item.ID] = true
		}
	}
	if len(action.ItemIDs) == 0 && action.FlowID == "" {
		return fmt.Errorf("explicit item IDs are required")
	}
	for _, id := range action.ItemIDs {
		if !known[id] {
			return fmt.Errorf("unknown comparison item: %s", id)
		}
	}
	if action.FlowID != "" {
		if action.Considered == nil || len(action.ItemIDs) > 0 || action.Status != "" {
			return fmt.Errorf("flow consideration must be an explicit separate action")
		}
		found := false
		for _, flow := range c.Flows {
			found = found || flow.ID == action.FlowID
		}
		if !found {
			return fmt.Errorf("unknown comparison flow")
		}
	}
	return nil
}
