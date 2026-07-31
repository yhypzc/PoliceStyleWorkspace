package main

import (
	"os"
	"sync"

	"golang.org/x/sys/windows"
)

const terminalPipeName = `\\.\pipe\PoliceStyleWorkspace-Terminal`

// terminalBroadcaster provides a live local stream to every connected GUI.
// Its bounded backlog lets a GUI opened later display recent output without
// using the audit log as a terminal transport.
type terminalBroadcaster struct {
	mu      sync.Mutex
	clients map[*os.File]struct{}
	backlog []byte
}

func newTerminalBroadcaster() *terminalBroadcaster {
	b := &terminalBroadcaster{clients: make(map[*os.File]struct{})}
	go b.acceptLoop()
	return b
}

func (b *terminalBroadcaster) Write(p []byte) (int, error) {
	b.mu.Lock()
	b.backlog = append(b.backlog, p...)
	const maxBacklog = 256 * 1024
	if len(b.backlog) > maxBacklog {
		b.backlog = append([]byte(nil), b.backlog[len(b.backlog)-maxBacklog:]...)
	}
	for client := range b.clients {
		if _, err := client.Write(p); err != nil {
			client.Close()
			delete(b.clients, client)
		}
	}
	b.mu.Unlock()
	return len(p), nil
}

func (b *terminalBroadcaster) acceptLoop() {
	name, _ := windows.UTF16PtrFromString(terminalPipeName)
	for {
		handle, err := windows.CreateNamedPipe(
			name,
			windows.PIPE_ACCESS_OUTBOUND,
			windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT,
			windows.PIPE_UNLIMITED_INSTANCES,
			64*1024,
			0,
			0,
			nil,
		)
		if err != nil {
			return
		}
		err = windows.ConnectNamedPipe(handle, nil)
		if err != nil && err != windows.ERROR_PIPE_CONNECTED {
			windows.CloseHandle(handle)
			continue
		}
		client := os.NewFile(uintptr(handle), terminalPipeName)
		b.mu.Lock()
		if len(b.backlog) > 0 {
			if _, err := client.Write(b.backlog); err != nil {
				client.Close()
				b.mu.Unlock()
				continue
			}
		}
		b.clients[client] = struct{}{}
		b.mu.Unlock()
	}
}
