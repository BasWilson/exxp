package main

import (
	"database/sql"
	"embed"
	"fmt"
	"html/template"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

//go:embed templates/*
var templatesFS embed.FS

type Task struct {
	ID        string
	Name      string
	XP        int
	Completed bool
}

type Unlockable struct {
	ID          string
	Level       int
	Description string
}

type templateData struct {
	SessionID    string
	Tasks        []Task
	Unlockables  []Unlockable
	TotalXP      int
	CurrentLevel int
	Progress     float64
	Unlocked     map[int]bool
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
	CREATE TABLE IF NOT EXISTS sessions (
		id VARCHAR(255) PRIMARY KEY,
		total_xp INTEGER NOT NULL DEFAULT 0,
		created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
	);

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
		created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
		session_id VARCHAR(255) REFERENCES sessions(id)
	);

	CREATE TABLE IF NOT EXISTS unlockables (
		id SERIAL PRIMARY KEY,
		level INTEGER NOT NULL CHECK (level >= 0),
		description TEXT NOT NULL,
		created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
		session_id VARCHAR(255) REFERENCES sessions(id),
		UNIQUE (level, description)
	);

	CREATE TABLE IF NOT EXISTS unlocked_levels (
		level INTEGER PRIMARY KEY,
		unlocked BOOLEAN NOT NULL DEFAULT true,
		unlocked_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
		session_id VARCHAR(255) REFERENCES sessions(id)
	);

	CREATE INDEX IF NOT EXISTS idx_tasks_completed ON tasks(completed);
	CREATE INDEX IF NOT EXISTS idx_unlockables_level ON unlockables(level);`
)

type AppState struct {
	sync.RWMutex
	db        *sql.DB
	SessionID string
	TotalXP   int
	Unlocked  map[int]bool
	stmts     map[string]*sql.Stmt
}

var templates *template.Template

func init() {
	godotenv.Load()
	templates = template.Must(template.New("").Funcs(template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
		"mod": func(a, b int) int { return a % b },
	}).ParseGlob("templates/*.html"))
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
func initAppState(db *sql.DB, sessionID string) (*AppState, error) {
	state := &AppState{
		db:        db,
		SessionID: sessionID,
		Unlocked:  make(map[int]bool),
		stmts:     make(map[string]*sql.Stmt),
	}

	// First ensure the session exists
	_, err := db.Exec("INSERT INTO sessions (id) VALUES ($1) ON CONFLICT (id) DO NOTHING", sessionID)
	if err != nil {
		return nil, fmt.Errorf("error ensuring session exists: %w", err)
	}

	// Prepare statements
	statements := map[string]string{
		"getTasks":        "SELECT id, name, xp, completed FROM tasks WHERE session_id = $1",
		"getUnlockables": "SELECT id, level, description FROM unlockables WHERE session_id = $1 ORDER BY level",
		"addTask":        "INSERT INTO tasks (name, xp, completed, session_id) VALUES ($1, $2, false, $3)",
		"completeTask":   "UPDATE tasks SET completed = true WHERE id = $1 AND session_id = $2 AND completed = false RETURNING xp",
		"updateXP":       "UPDATE sessions SET total_xp = $1 WHERE id = $2",
		"addUnlockable":  "INSERT INTO unlockables (level, description, session_id) VALUES ($1, $2, $3)",
	}

	for name, query := range statements {
		stmt, err := db.Prepare(query)
		if err != nil {
			return nil, fmt.Errorf("error preparing statement %s: %w", name, err)
		}
		state.stmts[name] = stmt
	}

	// Initialize state
	err = db.QueryRow("SELECT total_xp FROM sessions WHERE id = $1", sessionID).Scan(&state.TotalXP)
	if err != nil {
		return nil, fmt.Errorf("error loading session state: %w", err)
	}

	// Load unlocked levels
	if err := state.loadUnlockedLevels(); err != nil {
		return nil, err
	}

	return state, nil
}

func (a *AppState) loadUnlockedLevels() error {
	rows, err := a.db.Query("SELECT level FROM unlocked_levels WHERE session_id = $1 AND unlocked = true", a.SessionID)
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
	rows, err := a.stmts["getTasks"].Query(a.SessionID)
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
	rows, err := a.stmts["getUnlockables"].Query(a.SessionID)
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

// Add this function to generate random session IDs
func generateSessionID() string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	b := strings.Builder{}
	for i := 0; i < 4; i++ {
		b.WriteByte(letters[rand.Intn(len(letters))])
	}
	return b.String()
}

func main() {
	db, err := initDB(Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		ServerPort:  ":8080",
		MaxDBConns:  10,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		sessionID := strings.TrimPrefix(r.URL.Path, "/")
		
		if sessionID == "" {
			newSessionID := generateSessionID()
			http.Redirect(w, r, "/"+newSessionID, http.StatusFound)
			return
		}

		if err := initSession(db, sessionID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		state, err := initAppState(db, sessionID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if err := state.loadSessionXP(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		state.renderAppState(w, false)
	})

	http.HandleFunc("/add-task/", func(w http.ResponseWriter, r *http.Request) {
		sessionID := strings.TrimPrefix(r.URL.Path, "/add-task/")
		state, err := initAppState(db, sessionID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		state.handleAddTask(w, r)
	})

	http.HandleFunc("/add-xp/", func(w http.ResponseWriter, r *http.Request) {
		sessionID := strings.TrimPrefix(r.URL.Path, "/add-xp/")
		state, err := initAppState(db, sessionID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		state.handleAddXP(w, r)
	})

	http.HandleFunc("/add-unlockable/", func(w http.ResponseWriter, r *http.Request) {
		sessionID := strings.TrimPrefix(r.URL.Path, "/add-unlockable/")
		state, err := initAppState(db, sessionID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		state.handleAddUnlockable(w, r)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("Server starting on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func (a *AppState) GetProgressPercentage() int {
	currentLevelXP := a.TotalXP % 1000
	return int(float64(currentLevelXP) / 1000 * 100)
}

func initSession(db *sql.DB, sessionID string) error {
	_, err := db.Exec("INSERT INTO sessions (id) VALUES ($1) ON CONFLICT (id) DO NOTHING", sessionID)
	return err
}

func (s *AppState) loadSessionXP() error {
	return s.db.QueryRow("SELECT total_xp FROM sessions WHERE id = $1", s.SessionID).Scan(&s.TotalXP)
}

func (s *AppState) handleAddTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := r.FormValue("name")
	xp := r.FormValue("xp")
	
	_, err := s.stmts["addTask"].Exec(name, xp, s.SessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.renderAppState(w, true)
}

func (s *AppState) handleAddXP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	taskID := r.FormValue("task")
	
	tx, err := s.db.Begin()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	var xp int
	err = tx.QueryRow("UPDATE tasks SET completed = true WHERE id = $1 AND session_id = $2 AND completed = false RETURNING xp",
		taskID, s.SessionID).Scan(&xp)
	if err == sql.ErrNoRows {
		http.Error(w, "Task already completed or not found", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.TotalXP += xp

	_, err = tx.Exec("UPDATE sessions SET total_xp = $1 WHERE id = $2", s.TotalXP, s.SessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err = tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.renderAppState(w, true)
}

func (s *AppState) handleAddUnlockable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	level := r.FormValue("level")
	description := r.FormValue("description")
	
	_, err := s.stmts["addUnlockable"].Exec(level, description, s.SessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.renderAppState(w, true)
}

func (s *AppState) renderAppState(w http.ResponseWriter, onlyApp bool) {
	tasks, err := s.getTasks()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	unlockables, err := s.getUnlockables()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := templateData{
		SessionID:    s.SessionID,
		Tasks:        tasks,
		Unlockables:  unlockables,
		TotalXP:      s.TotalXP,
		CurrentLevel: s.TotalXP / 1000,
		Progress:     float64(s.TotalXP%1000) / 10.0,
		Unlocked:     s.Unlocked,
	}
	
	file := "index.html"
	if onlyApp {
		file = "app.html"
	}

	if err := templates.ExecuteTemplate(w, file, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}