package clipboard

import (
	"encoding/binary"
	"errors"
	"os"
	"sync"
	"syscall"
	"time"
)

// ioctl constants from linux/uinput.h
const (
	uiSetEvbit  = 0x40045564 // UI_SET_EVBIT
	uiSetKeybit = 0x40045565 // UI_SET_KEYBIT
	uiDevCreate = 0x5501     // UI_DEV_CREATE
)

// input event types from linux/input-event-codes.h
const (
	evSyn = 0x00
	evKey = 0x01
)

const busUSB = 0x03

type inputEvent struct {
	Time  syscall.Timeval
	Type  uint16
	Code  uint16
	Value int32
}

type inputID struct {
	Bustype uint16
	Vendor  uint16
	Product uint16
	Version uint16
}

type uinputUserDev struct {
	Name         [80]byte
	ID           inputID
	FfEffectsMax uint32
	Absmax       [64]int32
	Absmin       [64]int32
	Absfuzz      [64]int32
	Absflat      [64]int32
}

var (
	fd     *os.File
	fdOnce sync.Once
	fdErr  error
)

func Init() error {
	fdOnce.Do(func() {
		path := "/dev/uinput"
		if _, err := os.Stat(path); err != nil {
			path = "/dev/input/uinput"
			if _, err := os.Stat(path); err != nil {
				fdErr = errors.New("uinput device not found, try: sudo modprobe uinput")
				return
			}
		}
		f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, os.ModeDevice)
		if err != nil {
			fdErr = err
			return
		}
		// Set EV_KEY and EV_SYN capabilities
		if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uiSetEvbit, evKey); errno != 0 {
			fdErr = errno
			f.Close()
			return
		}
		if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uiSetEvbit, evSyn); errno != 0 {
			fdErr = errno
			f.Close()
			return
		}
		// Register all standard keys so udev classifies this as a keyboard
		for i := uintptr(0); i < 256; i++ {
			if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uiSetKeybit, i); errno != 0 {
				fdErr = errno
				f.Close()
				return
			}
		}
		// Create device
		dev := uinputUserDev{}
		copy(dev.Name[:], "zee-paste")
		dev.ID.Bustype = busUSB
		dev.ID.Vendor = 0x1234
		dev.ID.Product = 0x5678
		dev.ID.Version = 1
		if err := binary.Write(f, binary.LittleEndian, &dev); err != nil {
			fdErr = err
			f.Close()
			return
		}
		if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uiDevCreate, 0); errno != 0 {
			fdErr = errno
			f.Close()
			return
		}
		fd = f
		// Give compositor time to recognize the new input device
		time.Sleep(200 * time.Millisecond)
	})
	return fdErr
}

func writeEvent(typ, code uint16, value int32) error {
	ev := inputEvent{}
	ev.Type = typ
	ev.Code = code
	ev.Value = value
	return binary.Write(fd, binary.LittleEndian, &ev)
}

func syn() error {
	return writeEvent(evSyn, 0, 0)
}

func Paste() error {
	if err := Init(); err != nil {
		return err
	}
	// Ctrl down
	if err := writeEvent(evKey, 29, 1); err != nil {
		return err
	}
	if err := syn(); err != nil {
		return err
	}
	// Let compositor register modifier state
	time.Sleep(5 * time.Millisecond)
	// V down
	if err := writeEvent(evKey, 47, 1); err != nil {
		return err
	}
	if err := syn(); err != nil {
		return err
	}
	time.Sleep(5 * time.Millisecond)
	// V up
	if err := writeEvent(evKey, 47, 0); err != nil {
		return err
	}
	if err := syn(); err != nil {
		return err
	}
	time.Sleep(5 * time.Millisecond)
	// Ctrl up
	if err := writeEvent(evKey, 29, 0); err != nil {
		return err
	}
	return syn()
}
