package main

import (
	"embed"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"PoliceStyleWorkspace/handlers"
	"PoliceStyleWorkspace/middleware"
	"PoliceStyleWorkspace/models"
	"golang.org/x/sys/windows"
)

//go:embed static
var embeddedStatic embed.FS

func main() {
	instance, firstInstance := acquireServerInstance()
	if !firstInstance {
		return
	}
	defer windows.CloseHandle(instance)

	port := flag.Int("port", 3456, "HTTP service port")
	flag.Parse()

	baseDir := runtimeRoot()
	mustMkdir(filepath.Join(baseDir, "databases"))
	mustMkdir(filepath.Join(baseDir, "log"))
	mustMkdir(filepath.Join(baseDir, "config"))

	logFile, err := openRollingLog(filepath.Join(baseDir, "log", "server.log"))
	if err != nil {
		panic(err)
	}
	defer logFile.Close()
	// The audit file is independent from the live local terminal pipe. This
	// keeps GUI display real-time while preserving the log for later review.
	terminal := newTerminalBroadcaster()
	log.SetOutput(io.MultiWriter(logFile, terminal, os.Stdout))
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	db, initialPassword, err := models.Init(filepath.Join(baseDir, "databases", "police_style.db"))
	if err != nil {
		log.Fatalf("database init failed: %v", err)
	}
	defer db.Close()
	if initialPassword != "" {
		log.Printf("[系统] 初始密码: %s", initialPassword)
	}

	sessionStore := middleware.NewSessionStore(30 * time.Minute)
	app := handlers.NewApp(db, sessionStore, filepath.Join(baseDir, "log", "server.log"), filepath.Join(baseDir, "config"))
	app.StartDailyReportScheduler()

	staticFS, err := fs.Sub(embeddedStatic, "static")
	if err != nil {
		log.Fatalf("static fs init failed: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /students", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, staticFS, "students.html")
	})
	mux.HandleFunc("GET /deductions", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, staticFS, "deductions.html")
	})
	mux.HandleFunc("GET /multi-deductions", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, staticFS, "multi-deductions.html")
	})
	mux.HandleFunc("GET /semester", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, staticFS, "semester.html")
	})
	mux.HandleFunc("GET /dorms", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, staticFS, "dorms.html")
	})
	mux.HandleFunc("GET /daily-management", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, staticFS, "daily-management.html")
	})
	mux.HandleFunc("GET /daily-report", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, staticFS, "workspace.html")
	})
	mux.HandleFunc("/", spaHandler(staticFS))
	mux.HandleFunc("POST /api/login", app.Login)
	mux.Handle("POST /api/change-password", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.ChangePassword)))
	mux.Handle("GET /api/check-auth", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.CheckAuth)))
	mux.Handle("GET /api/clock", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.ServerClock)))
	mux.Handle("GET /api/workspace/stats", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.WorkspaceStats)))
	mux.Handle("GET /api/workspace/out-of-semester-records", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.ListOutOfSemesterRecords)))
	mux.Handle("GET /api/workspace/multi-without-subrecords-records", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.ListMultiWithoutSubrecordsWorkspaceRecords)))
	mux.Handle("GET /api/workspace/unassigned-records", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.ListUnassignedWorkspaceRecords)))
	mux.Handle("GET /api/daily-management/{name}/weeks/{week}", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.DailyManagementWeek)))
	mux.Handle("GET /api/daily-management/{name}/summary", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.DailyManagementSummary)))
	mux.Handle("GET /api/daily-management/{name}/weeks/{week}/export", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.ExportDailyManagementWeek)))
	mux.Handle("GET /api/daily-management/{name}/weeks/{week}/export-preferences", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.GetDailyExportPreferences)))
	mux.Handle("POST /api/daily-management/{name}/weeks/{week}/export-preferences", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.SaveDailyExportPreferences)))
	mux.Handle("GET /api/daily-management/{name}/summary/export", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.ExportDailyManagementSummary)))
	mux.Handle("GET /api/daily-report/config", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.GetDailyReportConfig)))
	mux.Handle("PUT /api/daily-report/config", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.SaveDailyReportConfig)))
	mux.Handle("GET /api/daily-report/robots", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.ListDingTalkRobots)))
	mux.Handle("POST /api/daily-report/robots", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.SaveDingTalkRobot)))
	mux.Handle("DELETE /api/daily-report/robots", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.DeleteDingTalkRobot)))
	mux.Handle("GET /api/daily-report/logs", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.ListDailyReportLogs)))
	mux.Handle("DELETE /api/daily-report/logs", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.DeleteDailyReportLog)))
	mux.Handle("GET /api/daily-report/logs/export", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.ExportDailyReportLog)))
	mux.Handle("POST /api/daily-report/run", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.RunDailyReportNow)))
	mux.Handle("POST /api/logout", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.Logout)))
	mux.Handle("GET /api/semesters", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.ListSemesters)))
	mux.Handle("POST /api/semesters", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.CreateSemester)))
	mux.Handle("GET /api/semesters/{name}", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.GetSemester)))
	mux.Handle("PUT /api/semesters/{name}", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.UpdateSemester)))
	mux.Handle("DELETE /api/semesters/{name}", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.DeleteSemester)))
	mux.Handle("GET /api/dorms", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.ListDorms)))
	mux.Handle("POST /api/dorms", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.CreateDorm)))
	mux.Handle("PUT /api/dorms/{name}", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.UpdateDorm)))
	mux.Handle("DELETE /api/dorms/{name}", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.DeleteDorm)))
	mux.Handle("PUT /api/dorms/reorder", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.ReorderDorms)))
	mux.Handle("GET /api/students", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.ListStudents)))
	mux.Handle("POST /api/students", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.CreateStudent)))
	mux.Handle("PUT /api/students/{id}", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.UpdateStudent)))
	mux.Handle("DELETE /api/students/{id}", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.DeleteStudent)))
	mux.Handle("POST /api/students/batch-delete", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.BatchDeleteStudents)))
	mux.Handle("POST /api/students/import", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.ImportStudents)))
	mux.Handle("GET /api/students/template", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.DownloadStudentTemplate)))
	mux.Handle("GET /api/deductions", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.ListDeductionRecords)))
	mux.Handle("POST /api/deductions", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.CreateDeductionRecord)))
	mux.Handle("PUT /api/deductions/{id}", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.UpdateDeductionRecord)))
	mux.Handle("PUT /api/deductions/{id}/recognition", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.UpdateDeductionRecognition)))
	mux.Handle("DELETE /api/deductions/{id}", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.DeleteDeductionRecord)))
	mux.Handle("POST /api/deductions/batch-delete", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.BatchDeleteDeductionRecords)))
	mux.Handle("POST /api/deductions/import", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.ImportDeductionRecords)))
	mux.Handle("GET /api/deductions/template", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.DownloadDeductionTemplate)))
	mux.Handle("GET /api/multi-deductions", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.ListMultiDeductions)))
	mux.Handle("PUT /api/multi-deductions/{id}", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.UpdateMultiDeduction)))
	mux.Handle("POST /api/multi-deductions/batch-delete", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.DeleteMultiDeductions)))
	mux.Handle("POST /api/multi-deductions/import", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.ImportMultiDeductions)))
	mux.Handle("GET /api/multi-deductions/template", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.DownloadMultiDeductionTemplate)))
	mux.Handle("GET /api/multi-deductions/{id}/subrecords", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.ListMultiDeductionSubrecords)))
	mux.Handle("PUT /api/multi-deductions/{id}/subrecords", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.SaveMultiDeductionSubrecord)))
	mux.Handle("DELETE /api/multi-deductions/{id}/subrecords/{subID}", middleware.RequireAuth(sessionStore, http.HandlerFunc(app.DeleteMultiDeductionSubrecord)))

	server := &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", *port),
		Handler:           loggingMiddleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	app.SetServer(server)
	apiStop := make(chan struct{})
	var stopOnce sync.Once
	app.SetStopFunc(func() { stopOnce.Do(func() { close(apiStop) }) })

	go func() {
		log.Printf("[系统] 纪检工作台服务启动，监听地址: 127.0.0.1:%d", *port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server failed: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case <-stop:
	case <-apiStop:
	}
	log.Println("[系统] 收到退出信号，服务关闭")
}

func executableDir() string {
	exe, err := os.Executable()
	if err != nil {
		wd, _ := os.Getwd()
		return wd
	}
	return filepath.Dir(exe)
}

// Release executables live in bin/, while mutable data belongs to the sibling
// databases/, config/, and log/ directories specified by the release layout.
func runtimeRoot() string {
	dir := executableDir()
	if strings.EqualFold(filepath.Base(dir), "bin") {
		return filepath.Dir(dir)
	}
	return dir
}

func mustMkdir(path string) {
	if err := os.MkdirAll(path, 0755); err != nil {
		panic(err)
	}
}

type rollingLog struct {
	mu        sync.Mutex
	path      string
	maxSize   int64
	file      *os.File
	size      int64
	pending   []byte
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

func openRollingLog(path string) (*rollingLog, error) {
	writer := &rollingLog{
		path: path, maxSize: 10 * 1024 * 1024,
		stop: make(chan struct{}), done: make(chan struct{}),
	}
	if err := writer.open(); err != nil {
		return nil, err
	}
	go writer.flushLoop()
	return writer, nil
}

func (w *rollingLog) open() error {
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return err
	}
	w.file, w.size = file, info.Size()
	return nil
}

func (w *rollingLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.size+int64(len(w.pending))+int64(len(p)) > w.maxSize {
		if err := w.flushLocked(); err != nil {
			return 0, err
		}
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	w.pending = append(w.pending, p...)
	return len(p), nil
}

func (w *rollingLog) flushLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	defer close(w.done)
	for {
		select {
		case <-ticker.C:
			w.mu.Lock()
			_ = w.flushLocked()
			w.mu.Unlock()
		case <-w.stop:
			return
		}
	}
}

func (w *rollingLog) flushLocked() error {
	if len(w.pending) == 0 {
		return nil
	}
	n, err := w.file.Write(w.pending)
	w.size += int64(n)
	w.pending = w.pending[n:]
	return err
}

func (w *rollingLog) rotate() error {
	if err := w.file.Close(); err != nil {
		return err
	}
	backup := w.path + "." + time.Now().Format("20060102150405")
	if err := os.Rename(w.path, backup); err != nil && !os.IsNotExist(err) {
		return err
	}
	return w.open()
}

func (w *rollingLog) Close() error {
	w.closeOnce.Do(func() { close(w.stop) })
	<-w.done
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	flushErr := w.flushLocked()
	closeErr := w.file.Close()
	w.file = nil
	if flushErr != nil {
		return flushErr
	}
	return closeErr
}

func spaHandler(staticFS fs.FS) http.HandlerFunc {
	fileServer := http.FileServer(http.FS(staticFS))
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" {
			http.ServeFileFS(w, r, staticFS, "login.html")
			return
		}
		if path == "/login" {
			http.ServeFileFS(w, r, staticFS, "login.html")
			return
		}
		if path == "/change-password" {
			http.ServeFileFS(w, r, staticFS, "change-password.html")
			return
		}
		if path == "/workspace" {
			http.ServeFileFS(w, r, staticFS, "workspace.html")
			return
		}
		if path == "/semester" {
			http.ServeFileFS(w, r, staticFS, "semester.html")
			return
		}
		if path == "/dorms" {
			http.ServeFileFS(w, r, staticFS, "dorms.html")
			return
		}
		if path == "/daily-management" {
			http.ServeFileFS(w, r, staticFS, "daily-management.html")
			return
		}
		if path == "/favicon.ico" {
			http.ServeFileFS(w, r, staticFS, "favicon.ico")
			return
		}
		fileServer.ServeHTTP(w, r)
	}
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("[访问] %s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}
