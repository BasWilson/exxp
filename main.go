package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"sync"
)

//go:embed templates/*
var templatesFS embed.FS

type Task struct {
	Name      string `json:"Name"`
	XP        int    `json:"XP"`
	Completed bool   `json:"Completed"`
}

type Unlockable struct {
	Level       int
	Description string
}

type AppState struct {
	sync.Mutex
	filename      string        // Add filename field
	TotalXP      int          `json:"totalXP"`
	Unlockables  []Unlockable `json:"unlockables"`
	Unlocked     map[int]bool `json:"unlocked"`
	Tasks        []Task       `json:"tasks"`
}

// Add methods to load and save state
func loadState(filename string) (*AppState, error) {
	state := &AppState{filename: filename}
	data, err := os.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			// Initialize with empty slices and maps if file doesn't exist
			state.Unlockables = []Unlockable{}
			state.Unlocked = make(map[int]bool)
			state.Tasks = []Task{}
			return state, state.save()
		}
		return nil, err
	}

	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}

	// Initialize maps if they're nil
	if state.Unlocked == nil {
		state.Unlocked = make(map[int]bool)
	}

	return state, nil
}

func (a *AppState) save() error {
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(a.filename, data, 0644)
}

func main() {
	state, err := loadState("appstate.json")
	if err != nil {
		log.Fatal(err)
	}

	funcMap := template.FuncMap{
		"add": func(a, b int) int {
			return a + b
		},
		"sub": func(a, b int) int {
			return a - b
		},
		"mod": func(a, b int) int {
			return a % b
		},
	}

	tmpl := template.Must(template.New("").Funcs(funcMap).ParseFS(templatesFS, "templates/*.html"))

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		state.Lock()
		defer state.Unlock()
		currentLevel := state.TotalXP / 1000
		err := tmpl.ExecuteTemplate(w, "index.html", map[string]interface{}{
			"TotalXP":     state.TotalXP,
			"CurrentLevel": currentLevel,
			"Tasks":       state.Tasks,
			"Unlockables": state.Unlockables,
			"Unlocked":    state.Unlocked,
			"Progress":    state.GetProgressPercentage(),
		})
		if err != nil {
			fmt.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	http.HandleFunc("/add-xp", func(w http.ResponseWriter, r *http.Request) {
		state.Lock()
		defer state.Unlock()
		
		taskIndex := r.FormValue("task")
		taskIndexInt := 0
		fmt.Sscanf(taskIndex, "%d", &taskIndexInt)

		// Check if task is already completed
		if state.Tasks[taskIndexInt].Completed {
			http.Error(w, "Task already completed", http.StatusBadRequest)
			return
		}

		var xp int
		for i, task := range state.Tasks {
			if fmt.Sprint(i) == taskIndex {
				xp = task.XP
				// Mark task as completed
				state.Tasks[i].Completed = true
				break
			}
		}

		previousLevel := state.TotalXP / 1000
		state.TotalXP += xp
		newLevel := state.TotalXP / 1000

		// Check for unlockables when level increases
		if newLevel > previousLevel {
			for _, u := range state.Unlockables {
				if u.Level > previousLevel && u.Level <= newLevel {
					state.Unlocked[u.Level] = true
				}
			}
		}

		// Save state after modification
		if err := state.save(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		currentLevel := newLevel
		tmpl.ExecuteTemplate(w, "app.html", map[string]interface{}{
			"TotalXP":     state.TotalXP,
			"CurrentLevel": currentLevel,
			"Tasks":       state.Tasks,
			"Unlockables": state.Unlockables,
			"Unlocked":    state.Unlocked,
			"Progress":    state.GetProgressPercentage(),
		})
	})

	// Add new endpoint for creating unlockables
	http.HandleFunc("/add-unlockable", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		state.Lock()
		defer state.Unlock()

		var level int
		fmt.Sscanf(r.FormValue("level"), "%d", &level)
		description := r.FormValue("description")

		if description == "" {
			http.Error(w, "Description is required", http.StatusBadRequest)
			return
		}

		if level < 0 {
			http.Error(w, "Level must be positive", http.StatusBadRequest)
			return
		}

		newUnlockable := Unlockable{
			Level:       level,
			Description: description,
		}

		state.Unlockables = append(state.Unlockables, newUnlockable)

		// Check if it should be unlocked based on current level
		currentLevel := state.TotalXP / 1000
		if level <= currentLevel {
			state.Unlocked[level] = true
		}

		// Save state after modification
		if err := state.save(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		tmpl.ExecuteTemplate(w, "app.html", map[string]interface{}{
			"TotalXP":      state.TotalXP,
			"CurrentLevel": currentLevel,
			"Tasks":        state.Tasks,
			"Unlockables":  state.Unlockables,
			"Unlocked":     state.Unlocked,
			"Progress":     state.GetProgressPercentage(),
		})
	})

	// Add new endpoint for creating tasks
	http.HandleFunc("/add-task", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		state.Lock()
		defer state.Unlock()

		name := r.FormValue("name")
		var xp int
		fmt.Sscanf(r.FormValue("xp"), "%d", &xp)

		if name == "" {
			http.Error(w, "Task name is required", http.StatusBadRequest)
			return
		}

		if xp <= 0 {
			http.Error(w, "XP must be positive", http.StatusBadRequest)
			return
		}

		newTask := Task{
			Name:      name,
			XP:        xp,
			Completed: false,
		}

		state.Tasks = append(state.Tasks, newTask)

		// Save state after modification
		if err := state.save(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		tmpl.ExecuteTemplate(w, "app.html", map[string]interface{}{
			"TotalXP":      state.TotalXP,
			"CurrentLevel": state.TotalXP / 1000,
			"Tasks":        state.Tasks,
			"Unlockables":  state.Unlockables,
			"Unlocked":     state.Unlocked,
			"Progress":     state.GetProgressPercentage(),
		})
	})

	http.ListenAndServe(":8080", nil)
}

func (a *AppState) GetProgressPercentage() int {
	currentLevelXP := a.TotalXP % 1000
	return int(float64(currentLevelXP) / 1000 * 100)
}