package adapter

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/clawssh/clawssh/pkg/dsl"
	"golang.org/x/crypto/ssh"
)

type RemoteSSHConfig struct {
	DefaultUser      string
	DefaultHost      string
	DefaultPort      int
	PrivateKeyPath   string
	PrivateKeyPEM    string
	DefaultPassword  string
	ConnectTimeout   time.Duration
	IdleTTL          time.Duration
	MaxConnsPerRoute int
}

type pooledClient struct {
	client   *ssh.Client
	lastUsed time.Time
}

// RemoteSSHProvider executes tasks on remote hosts over SSH with a simple connection pool.
type RemoteSSHProvider struct {
	cfg  RemoteSSHConfig
	pool map[string][]*pooledClient
	mu   sync.Mutex
}

func (r *RemoteSSHProvider) ConnectionPoolStats() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	totalIdle := 0
	perRoute := make(map[string]int, len(r.pool))
	for route, list := range r.pool {
		n := len(list)
		perRoute[route] = n
		totalIdle += n
	}
	return map[string]any{
		"type":       "remote_ssh",
		"routes":     len(r.pool),
		"idle_total": totalIdle,
		"per_route":  perRoute,
	}
}

func NewRemoteSSHProviderFromEnv() (*RemoteSSHProvider, error) {
	port := 22
	if s := strings.TrimSpace(os.Getenv("CLAWSSH_REMOTE_SSH_PORT")); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 && v <= 65535 {
			port = v
		}
	}
	cfg := RemoteSSHConfig{
		DefaultUser:      strings.TrimSpace(os.Getenv("CLAWSSH_REMOTE_SSH_USER")),
		DefaultHost:      strings.TrimSpace(os.Getenv("CLAWSSH_REMOTE_SSH_HOST")),
		DefaultPort:      port,
		PrivateKeyPath:   strings.TrimSpace(os.Getenv("CLAWSSH_REMOTE_SSH_KEY_PATH")),
		PrivateKeyPEM:    os.Getenv("CLAWSSH_REMOTE_SSH_KEY"),
		DefaultPassword:  os.Getenv("CLAWSSH_REMOTE_SSH_PASSWORD"),
		ConnectTimeout:   15 * time.Second,
		IdleTTL:          2 * time.Minute,
		MaxConnsPerRoute: 4,
	}
	return NewRemoteSSHProvider(cfg)
}

func NewRemoteSSHProvider(cfg RemoteSSHConfig) (*RemoteSSHProvider, error) {
	if cfg.DefaultPort <= 0 {
		cfg.DefaultPort = 22
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 15 * time.Second
	}
	if cfg.IdleTTL <= 0 {
		cfg.IdleTTL = 2 * time.Minute
	}
	if cfg.MaxConnsPerRoute <= 0 {
		cfg.MaxConnsPerRoute = 4
	}
	return &RemoteSSHProvider{
		cfg:  cfg,
		pool: make(map[string][]*pooledClient),
	}, nil
}

func (r *RemoteSSHProvider) Execute(task *dsl.Task) (*dsl.Result, error) {
	if task == nil {
		return nil, newExecError(ErrCodeInvalidInput, "task is nil", nil)
	}
	host := firstNonEmpty(paramString(task.Parameters, "host"), r.cfg.DefaultHost)
	user := firstNonEmpty(paramString(task.Parameters, "user"), r.cfg.DefaultUser)
	port := r.cfg.DefaultPort
	if s := paramString(task.Parameters, "port"); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil || v <= 0 || v > 65535 {
			return &dsl.Result{OK: false, ExitCode: -1, Message: "invalid parameters.port"}, newExecError(ErrCodeInvalidInput, "invalid ssh port", err)
		}
		port = v
	}
	if host == "" || user == "" {
		msg := "remote ssh requires host and user (parameters or env)"
		return &dsl.Result{OK: false, ExitCode: -1, Message: msg}, newExecError(ErrCodeInvalidInput, msg, nil)
	}

	keyPath := firstNonEmpty(paramString(task.Parameters, "key_path"), r.cfg.PrivateKeyPath)
	keyPEM := firstNonEmpty(paramString(task.Parameters, "key"), r.cfg.PrivateKeyPEM)
	password := firstNonEmpty(paramString(task.Parameters, "password"), r.cfg.DefaultPassword)
	authMethods, err := buildAuthMethods(keyPath, keyPEM, password)
	if err != nil {
		return &dsl.Result{OK: false, ExitCode: -1, Message: "missing or invalid ssh credentials"}, newExecError(ErrCodeAuthFailed, "build ssh auth methods failed", err)
	}

	cmd, err := buildRemoteCommand(task)
	if err != nil {
		return &dsl.Result{OK: false, ExitCode: -1, Message: err.Error()}, err
	}
	route := fmt.Sprintf("%s@%s:%d", user, host, port)
	client, err := r.getClient(route, user, host, port, authMethods)
	if err != nil {
		return &dsl.Result{OK: false, ExitCode: -1, Message: err.Error()}, err
	}

	sess, err := client.NewSession()
	if err != nil {
		r.dropClient(route, client)
		return &dsl.Result{OK: false, ExitCode: -1, Message: "open ssh session failed"}, newExecError(ErrCodeConnectionFailed, "open ssh session failed", err)
	}
	defer sess.Close()

	var stdout, stderr bytes.Buffer
	sess.Stdout = &stdout
	sess.Stderr = &stderr
	runErr := sess.Run(cmd)
	r.releaseClient(route, client)

	exitCode := 0
	if runErr != nil {
		var ee *ssh.ExitError
		if ok := asExitErr(runErr, &ee); ok {
			exitCode = ee.ExitStatus()
		} else {
			return &dsl.Result{
					OK: false, ExitCode: -1, Stdout: stdout.String(), Stderr: stderr.String(), Message: runErr.Error(),
				},
				newExecError(ErrCodeExecFailed, "remote command execution failed", runErr)
		}
	}
	res := &dsl.Result{
		OK:       runErr == nil,
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}
	if runErr != nil {
		res.Message = runErr.Error()
	}
	return res, nil
}

// ExecuteStream runs command remotely and forwards live stdout/stderr to writers.
func (r *RemoteSSHProvider) ExecuteStream(task *dsl.Task, stdoutW, stderrW io.Writer) (*dsl.Result, error) {
	if task == nil {
		return nil, newExecError(ErrCodeInvalidInput, "task is nil", nil)
	}
	host := firstNonEmpty(paramString(task.Parameters, "host"), r.cfg.DefaultHost)
	user := firstNonEmpty(paramString(task.Parameters, "user"), r.cfg.DefaultUser)
	port := r.cfg.DefaultPort
	if s := paramString(task.Parameters, "port"); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil || v <= 0 || v > 65535 {
			return &dsl.Result{OK: false, ExitCode: -1, Message: "invalid parameters.port"}, newExecError(ErrCodeInvalidInput, "invalid ssh port", err)
		}
		port = v
	}
	if host == "" || user == "" {
		msg := "remote ssh requires host and user (parameters or env)"
		return &dsl.Result{OK: false, ExitCode: -1, Message: msg}, newExecError(ErrCodeInvalidInput, msg, nil)
	}
	keyPath := firstNonEmpty(paramString(task.Parameters, "key_path"), r.cfg.PrivateKeyPath)
	keyPEM := firstNonEmpty(paramString(task.Parameters, "key"), r.cfg.PrivateKeyPEM)
	password := firstNonEmpty(paramString(task.Parameters, "password"), r.cfg.DefaultPassword)
	authMethods, err := buildAuthMethods(keyPath, keyPEM, password)
	if err != nil {
		return &dsl.Result{OK: false, ExitCode: -1, Message: "missing or invalid ssh credentials"}, newExecError(ErrCodeAuthFailed, "build ssh auth methods failed", err)
	}
	cmd, err := buildRemoteCommand(task)
	if err != nil {
		return &dsl.Result{OK: false, ExitCode: -1, Message: err.Error()}, err
	}
	route := fmt.Sprintf("%s@%s:%d", user, host, port)
	client, err := r.getClient(route, user, host, port, authMethods)
	if err != nil {
		return &dsl.Result{OK: false, ExitCode: -1, Message: err.Error()}, err
	}
	sess, err := client.NewSession()
	if err != nil {
		r.dropClient(route, client)
		return &dsl.Result{OK: false, ExitCode: -1, Message: "open ssh session failed"}, newExecError(ErrCodeConnectionFailed, "open ssh session failed", err)
	}
	defer sess.Close()

	stdoutPipe, err := sess.StdoutPipe()
	if err != nil {
		r.dropClient(route, client)
		return &dsl.Result{OK: false, ExitCode: -1, Message: "open stdout pipe failed"}, newExecError(ErrCodeExecFailed, "open stdout pipe failed", err)
	}
	stderrPipe, err := sess.StderrPipe()
	if err != nil {
		r.dropClient(route, client)
		return &dsl.Result{OK: false, ExitCode: -1, Message: "open stderr pipe failed"}, newExecError(ErrCodeExecFailed, "open stderr pipe failed", err)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	if stdoutW == nil {
		stdoutW = io.Discard
	}
	if stderrW == nil {
		stderrW = io.Discard
	}
	stdoutDst := io.MultiWriter(stdoutW, &stdoutBuf)
	stderrDst := io.MultiWriter(stderrW, &stderrBuf)

	if err := sess.Start(cmd); err != nil {
		r.dropClient(route, client)
		return &dsl.Result{OK: false, ExitCode: -1, Message: "start remote command failed"}, newExecError(ErrCodeExecFailed, "start remote command failed", err)
	}

	copyDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(stdoutDst, stdoutPipe)
		close(copyDone)
	}()
	_, _ = io.Copy(stderrDst, stderrPipe)
	<-copyDone

	runErr := sess.Wait()
	r.releaseClient(route, client)
	exitCode := 0
	if runErr != nil {
		var ee *ssh.ExitError
		if ok := asExitErr(runErr, &ee); ok {
			exitCode = ee.ExitStatus()
		} else {
			return &dsl.Result{
					OK: false, ExitCode: -1, Stdout: stdoutBuf.String(), Stderr: stderrBuf.String(), Message: runErr.Error(),
				},
				newExecError(ErrCodeExecFailed, "remote command execution failed", runErr)
		}
	}
	res := &dsl.Result{
		OK:       runErr == nil,
		ExitCode: exitCode,
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
	}
	if runErr != nil {
		res.Message = runErr.Error()
	}
	return res, nil
}

func (r *RemoteSSHProvider) getClient(route, user, host string, port int, authMethods []ssh.AuthMethod) (*ssh.Client, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gcLocked()
	list := r.pool[route]
	now := time.Now()
	if len(list) > 0 {
		pc := list[len(list)-1]
		r.pool[route] = list[:len(list)-1]
		pc.lastUsed = now
		return pc.client, nil
	}
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         r.cfg.ConnectTimeout,
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, classifyDialError(err)
	}
	return client, nil
}

func (r *RemoteSSHProvider) releaseClient(route string, client *ssh.Client) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if client == nil {
		return
	}
	list := r.pool[route]
	if len(list) >= r.cfg.MaxConnsPerRoute {
		_ = client.Close()
		return
	}
	r.pool[route] = append(list, &pooledClient{client: client, lastUsed: time.Now()})
}

func (r *RemoteSSHProvider) dropClient(route string, client *ssh.Client) {
	_ = client.Close()
}

func (r *RemoteSSHProvider) gcLocked() {
	now := time.Now()
	for route, list := range r.pool {
		keep := list[:0]
		for _, pc := range list {
			if now.Sub(pc.lastUsed) > r.cfg.IdleTTL {
				_ = pc.client.Close()
				continue
			}
			keep = append(keep, pc)
		}
		if len(keep) == 0 {
			delete(r.pool, route)
		} else {
			r.pool[route] = keep
		}
	}
}

func loadSigner(keyPath, keyPEM string) (ssh.Signer, error) {
	var pem []byte
	if strings.TrimSpace(keyPEM) != "" {
		pem = []byte(keyPEM)
	} else if strings.TrimSpace(keyPath) != "" {
		b, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, err
		}
		pem = b
	} else {
		return nil, fmt.Errorf("missing private key (set CLAWSSH_REMOTE_SSH_KEY_PATH or CLAWSSH_REMOTE_SSH_KEY)")
	}
	return ssh.ParsePrivateKey(pem)
}

func buildAuthMethods(keyPath, keyPEM, password string) ([]ssh.AuthMethod, error) {
	methods := make([]ssh.AuthMethod, 0, 2)
	if strings.TrimSpace(keyPath) != "" || strings.TrimSpace(keyPEM) != "" {
		signer, err := loadSigner(keyPath, keyPEM)
		if err != nil {
			return nil, err
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if strings.TrimSpace(password) != "" {
		methods = append(methods, ssh.Password(password))
	}
	if len(methods) == 0 {
		return nil, fmt.Errorf("no auth method configured (set key or password)")
	}
	return methods, nil
}

func buildRemoteCommand(task *dsl.Task) (string, error) {
	switch task.Action {
	case dsl.ActionCheckCPU:
		return "top -bn1", nil
	case dsl.ActionCheckDisk:
		return "df -h", nil
	case dsl.ActionCheckMemory:
		return "free -m", nil
	case dsl.ActionServiceStatus:
		svc := paramString(task.Parameters, "service")
		if svc == "" {
			return "", newExecError(ErrCodeInvalidInput, "service_status requires parameters.service", nil)
		}
		return "systemctl status " + shellEscape(svc) + " --no-pager", nil
	case dsl.ActionTailLog:
		p := paramString(task.Parameters, "path")
		if p == "" {
			return "", newExecError(ErrCodeInvalidInput, "tail_log requires parameters.path", nil)
		}
		n := paramString(task.Parameters, "lines")
		if n == "" {
			n = "50"
		}
		return "tail -n " + shellEscape(n) + " " + shellEscape(p), nil
	case dsl.ActionReadFile:
		p := paramString(task.Parameters, "path")
		if p == "" {
			return "", newExecError(ErrCodeInvalidInput, "read_file requires parameters.path", nil)
		}
		return "head -n 200 " + shellEscape(p), nil
	default:
		msg := fmt.Sprintf("action %q is not implemented on RemoteSSHProvider", task.Action)
		return "", newExecError(ErrCodeUnsupportedAction, msg, nil)
	}
}

func shellEscape(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func classifyDialError(err error) error {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "unable to authenticate") || strings.Contains(msg, "handshake failed") {
		return newExecError(ErrCodeAuthFailed, "ssh authentication failed", err)
	}
	if strings.Contains(msg, "i/o timeout") || strings.Contains(msg, "deadline exceeded") {
		return newExecError(ErrCodeTimeout, "ssh connection timeout", err)
	}
	return newExecError(ErrCodeConnectionFailed, "ssh dial failed", err)
}

func asExitErr(err error, out **ssh.ExitError) bool {
	ee, ok := err.(*ssh.ExitError)
	if ok {
		*out = ee
		return true
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
