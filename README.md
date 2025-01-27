# XP Tracker 📈

A simple web application to track experience points (XP) and unlock achievements as you complete tasks. Built with Go, HTMX, and Tailwind CSS.

## Features

-   🎯 Create and complete tasks to earn XP
-   📊 Track progress with a visual XP bar
-   🏆 Define and unlock achievements at different levels
-   🔄 Real-time updates using HTMX
-   💾 Persistent storage using JSON

## Tech Stack

-   **Backend**: Go
-   **Frontend**: HTMX + Tailwind CSS
-   **Deployment**: Fly.io
-   **Storage**: Local JSON file

## Getting Started

1. Make sure you have Go 1.22.2 or later installed
2. Clone the repository
3. Run the application:

```bash
go run main.go
```

The application will be available at `http://localhost:8080`

## How It Works

-   Each task has an XP value
-   Completing tasks adds XP to your total
-   Every 1000 XP equals one level
-   Unlockables become available when you reach their required level
-   Progress is automatically saved to `appstate.json`

## Deployment

The application is configured to deploy on Fly.io. To deploy:

1. Install the Fly.io CLI
2. Authenticate with Fly.io
3. Deploy using:

```bash
fly deploy
```

## Project Structure

-   `main.go` - Main application logic and HTTP handlers
-   `appstate.json` - Application state storage
-   `templates/` - HTML templates
    -   `index.html` - Base template
    -   `app.html` - Main application layout
    -   `tasks.html` - Task list component
    -   `progress.html` - XP progress bar
    -   `unlockables.html` - Unlockables list

## License

MIT License

## Contributing

Feel free to open issues or submit pull requests for any improvements or bug fixes.
