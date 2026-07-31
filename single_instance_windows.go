package main

import "golang.org/x/sys/windows"

const serverMutexName = `Local\PoliceStyleWorkspaceServer`

func acquireServerInstance() (windows.Handle, bool) {
	name, _ := windows.UTF16PtrFromString(serverMutexName)
	handle, err := windows.CreateMutex(nil, false, name)
	if err == windows.ERROR_ALREADY_EXISTS {
		if handle != 0 {
			windows.CloseHandle(handle)
		}
		return 0, false
	}
	return handle, err == nil
}
