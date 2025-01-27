package main

import (
	"database/sql"
	"embed"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

//go:embed templates/*
var templatesFS embed.FS

type Task struct {
	ID        int
	Name      string
	XP        int
	Completed bool
}

type Unlockable struct {
	ID          int
	Level       int
	Description string
}

// Configuration struct
type Config struct {
	DatabaseURL string
	ServerPort  string
	MaxDBConns  int
}

// Database tables structure
const (
	createTablesSQL = `
	CREATE TABLE IF NOT EXISTS app_state (
		id INTEGER PRIMARY KEY,
		total_xp INTEGER NOT NULL DEFAULT 0,
		CHECK (id = 1)
	);

	CREATE TABLE IF NOT EXISTS tasks (
		id SERIAL PRIMARY KEY,
		name VARCHAR(255) NOT NULL,
		xp INTEGER NOT NULL CHECK (xp > 0),
		completed BOOLEAN NOT NULL DEFAULT false,
		created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS unlockables (
		id SERIAL PRIMARY KEY,
		level INTEGER NOT NULL CHECK (level >= 0),
		description TEXT NOT NULL,
		created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
		UNIQUE (level, description)
	);

	CREATE TABLE IF NOT EXISTS unlocked_levels (
		level INTEGER PRIMARY KEY,
		unlocked BOOLEAN NOT NULL DEFAULT true,
		unlocked_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_tasks_completed ON tasks(completed);
	CREATE INDEX IF NOT EXISTS idx_unlockables_level ON unlockables(level);`
)

type AppState struct {
	sync.RWMutex // Using RWMutex for better concurrent access
	db           *sql.DB
	TotalXP      int
	Unlocked     map[int]bool
	stmts        map[string]*sql.Stmt
}

// Initialize database connection
func initDB(config Config) (*sql.DB, error) {
	db, err := sql.Open("postgres", config.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("error opening database: %w", err)
	}

	// Set connection pool parameters
	db.SetMaxOpenConns(config.MaxDBConns)
	db.SetMaxIdleConns(config.MaxDBConns / 2)
	db.SetConnMaxLifetime(time.Hour)

	// Initialize tables
	if _, err := db.Exec(createTablesSQL); err != nil {
		return nil, fmt.Errorf("error creating tables: %w", err)
	}

	return db, nil
}

// Replace loadState with initAppState
func initAppState(db *sql.DB) (*AppState, error) {
	state := &AppState{
		db:       db,
		Unlocked: make(map[int]bool),
		stmts:    make(map[string]*sql.Stmt),
	}

	// Prepare statements
	statements := map[string]string{
		"getTasks":        "SELECT id, name, xp, completed FROM tasks",
		"getUnlockables": "SELECT id, level, description FROM unlockables",
		"addTask":        "INSERT INTO tasks (name, xp, completed) VALUES ($1, $2, false)",
		"completeTask":   "UPDATE tasks SET completed = true WHERE id = $1 AND completed = false RETURNING xp",
		"updateXP":       "UPDATE app_state SET total_xp = $1 WHERE id = 1",
		"addUnlockable":  "INSERT INTO unlockables (level, description) VALUES ($1, $2)",
	}

	for name, query := range statements {
		stmt, err := db.Prepare(query)
		if err != nil {
			return nil, fmt.Errorf("error preparing statement %s: %w", name, err)
		}
		state.stmts[name] = stmt
	}

	// Initialize state
	err := db.QueryRow("SELECT total_xp FROM app_state WHERE id = 1").Scan(&state.TotalXP)
	if err == sql.ErrNoRows {
		if _, err := db.Exec("INSERT INTO app_state (id, total_xp) VALUES (1, 0)"); err != nil {
			return nil, fmt.Errorf("error initializing app_state: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("error loading total XP: %w", err)
	}

	// Load unlocked levels
	if err := state.loadUnlockedLevels(); err != nil {
		return nil, err
	}

	return state, nil
}

func (a *AppState) loadUnlockedLevels() error {
	rows, err := a.db.Query("SELECT level FROM unlocked_levels WHERE unlocked = true")
	if err != nil {
		return fmt.Errorf("error loading unlocked levels: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var level int
		if err := rows.Scan(&level); err != nil {
			return fmt.Errorf("error scanning unlocked level: %w", err)
		}
		a.Unlocked[level] = true
	}
	return nil
}

// Use prepared statements and caching for frequently accessed methods
func (a *AppState) getTasks() ([]Task, error) {
	rows, err := a.stmts["getTasks"].Query()
	if err != nil {
		return nil, fmt.Errorf("error getting tasks: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.Name, &t.XP, &t.Completed); err != nil {
			return nil, fmt.Errorf("error scanning task: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

func (a *AppState) getUnlockables() ([]Unlockable, error) {
	rows, err := a.db.Query("SELECT id, level, description FROM unlockables")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var unlockables []Unlockable
	for rows.Next() {
		var u Unlockable
		if err := rows.Scan(&u.ID, &u.Level, &u.Description); err != nil {
			return nil, err
		}
		unlockables = append(unlockables, u)
	}
	return unlockables, nil
}

func main() {

	// load env
	godotenv.Load()
	
	db, err := initDB(Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		ServerPort:  ":8080",
		MaxDBConns:  10,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	state, err := initAppState(db)
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
		
		tasks, err := state.getTasks()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		
		unlockables, err := state.getUnlockables()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		currentLevel := state.TotalXP / 1000
		err = tmpl.ExecuteTemplate(w, "index.html", map[string]interface{}{
			"TotalXP":     state.TotalXP,
			"CurrentLevel": currentLevel,
			"Tasks":       tasks,
			"Unlockables": unlockables,
			"Unlocked":    state.Unlocked,
			"Progress":    state.GetProgressPercentage(),
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

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

		if name == "" || xp <= 0 {
			http.Error(w, "Invalid input", http.StatusBadRequest)
			return
		}

		// Use prepared statement
		_, err := state.stmts["addTask"].Exec(name, xp)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		
		tasks, err := state.getTasks()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		
		unlockables, err := state.getUnlockables()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		currentLevel := state.TotalXP / 1000
		err = tmpl.ExecuteTemplate(w, "app.html", map[string]interface{}{
			"TotalXP":     state.TotalXP,
			"CurrentLevel": currentLevel,
			"Tasks":       tasks,
			"Unlockables": unlockables,
			"Unlocked":    state.Unlocked,
			"Progress":    state.GetProgressPercentage(),
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	http.HandleFunc("/add-xp", func(w http.ResponseWriter, r *http.Request) {
		state.Lock()
		defer state.Unlock()

		taskID := r.FormValue("task")
		
		// Start transaction
		tx, err := state.db.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer tx.Rollback()

		// Get task XP and mark as completed
		var xp int
		err = tx.QueryRow("UPDATE tasks SET completed = true WHERE id = $1 AND completed = false RETURNING xp", taskID).Scan(&xp)
		if err == sql.ErrNoRows {
			http.Error(w, "Task already completed or not found", http.StatusBadRequest)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		previousLevel := state.TotalXP / 1000
		state.TotalXP += xp
		newLevel := state.TotalXP / 1000

		// Update total XP
		_, err = tx.Exec("UPDATE app_state SET total_xp = $1 WHERE id = 1", state.TotalXP)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Handle level-ups
		if newLevel > previousLevel {
			for level := previousLevel + 1; level <= newLevel; level++ {
				_, err = tx.Exec("INSERT INTO unlocked_levels (level, unlocked) VALUES ($1, true)", level)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				state.Unlocked[level] = true
			}
		}

		if err := tx.Commit(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		currentLevel := state.TotalXP / 1000
		tasks, err := state.getTasks()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		unlockables, err := state.getUnlockables()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		err = tmpl.ExecuteTemplate(w, "app.html", map[string]interface{}{
			"TotalXP":     state.TotalXP,
			"CurrentLevel": currentLevel,
			"Tasks":       tasks,
			"Unlockables": unlockables,
			"Unlocked":    state.Unlocked,
			"Progress":    state.GetProgressPercentage(),
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
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

		_, err := state.db.Exec("INSERT INTO unlockables (level, description) VALUES ($1, $2)", level, description)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Replace the comment with actual rendering
		tasks, err := state.getTasks()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		
		unlockables, err := state.getUnlockables()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		currentLevel := state.TotalXP / 1000
		err = tmpl.ExecuteTemplate(w, "index.html", map[string]interface{}{
			"TotalXP":     state.TotalXP,
			"CurrentLevel": currentLevel,
			"Tasks":       tasks,
			"Unlockables": unlockables,
			"Unlocked":    state.Unlocked,
			"Progress":    state.GetProgressPercentage(),
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	http.ListenAndServe(":8080", nil)
}

func (a *AppState) GetProgressPercentage() int {
	currentLevelXP := a.TotalXP % 1000
	return int(float64(currentLevelXP) / 1000 * 100)
}