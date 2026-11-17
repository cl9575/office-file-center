//go:build windows

package service

import (
	"context"
	"log"
	"time"

	"golang.org/x/sys/windows/svc"
)

func IsWindowsService() (bool, error) {
	return svc.IsWindowsService()
}

func Run(name string, run func(context.Context) error) error {
	return svc.Run(name, &handler{run: run})
}

type handler struct {
	run func(context.Context) error
}

func (h *handler) Execute(args []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	status <- svc.Status{State: svc.StartPending}
	go func() { done <- h.run(ctx) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for {
		select {
		case err := <-done:
			status <- svc.Status{State: svc.Stopped}
			if err != nil {
				log.Printf("服务退出：%v", err)
				return false, 1
			}
			return false, 0
		case req := <-requests:
			switch req.Cmd {
			case svc.Interrogate:
				status <- req.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				select {
				case err := <-done:
					status <- svc.Status{State: svc.Stopped}
					if err != nil {
						log.Printf("服务停止时出错：%v", err)
						return false, 1
					}
					return false, 0
				case <-time.After(35 * time.Second):
					log.Printf("服务停止超时")
					return false, 1
				}
			}
		}
	}
}
