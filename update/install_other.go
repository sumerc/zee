//go:build !darwin

package update

import "fmt"

func Install(Release) error {
	return fmt.Errorf("in-app updates are currently supported only on macOS")
}
