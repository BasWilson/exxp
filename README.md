# XP Tracker 📈

A simple web application to track experience points (XP) and unlock achievements as you complete tasks. Built with Go, HTMX, and Tailwind CSS.

## Features

-   🎯 Create and complete tasks to earn XP
-   📊 Track progress with a visual XP bar
-   🏆 Define and unlock achievements at different levels
-   🔄 Real-time updates using HTMX
-   💾 Persistent storage using PostgreSQL

## Tech Stack

-   **Backend**: Go
-   **Frontend**: HTMX + Tailwind CSS
-   **Deployment**: Fly.io
-   **Storage**: PostgreSQL

## Getting Started

1. Make sure you have Go 1.22.2 or later installed
2. Clone the repository
3. Create a PostgreSQL database
4. Create a `.env` file with your database connection string:

```env
DATABASE_URL=postgres://username:password@localhost:5432/dbname?sslmode=disable
```

5. Run the SQL schema to create the tables:

```sql
CREATE TABLE tasks (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    xp INTEGER NOT NULL,
    completed BOOLEAN DEFAULT FALSE
);

CREATE TABLE unlockables (
    id SERIAL PRIMARY KEY,
    level INTEGER NOT NULL,
    description TEXT NOT NULL
);

CREATE TABLE app_state (
    id INTEGER PRIMARY KEY DEFAULT 1,
    total_xp INTEGER DEFAULT 0,
    CHECK (id = 1)
);

CREATE TABLE unlocked_levels (
    level INTEGER PRIMARY KEY,
    unlocked BOOLEAN DEFAULT TRUE
);
```

6. Run the application:

```bash
go run main.go
```

The application will be available at `http://localhost:8080`

## How It Works

-   Each task has an XP value
-   Completing tasks adds XP to your total
-   Every 1000 XP equals one level
-   Unlockables become available when you reach their required level
-   Progress is automatically saved to PostgreSQL

## Deployment

The application is configured to deploy on Fly.io. To deploy:

1. Install the Fly.io CLI
2. Authenticate with Fly.io
3. Set up your PostgreSQL database URL as a secret:

```bash
fly secrets set DATABASE_URL="postgres://username:password@host:5432/dbname"
```

4. Deploy using:

```bash
fly deploy
```

## Project Structure

-   `main.go` - Main application logic and HTTP handlers
-   `templates/` - HTML templates
    -   `index.html` - Base template
    -   `app.html` - Main application layout
    -   `tasks.html` - Task list component
    -   `progress.html` - XP progress bar
    -   `unlockables.html` - Unlockables list
    -   `add_task.html` - Task creation form

## License

MIT License

## Contributing

Feel free to open issues or submit pull requests for any improvements or bug fixes.
