// +build !linux

package dmcache

import (
	"github.com/yevheniir/telegraf-fork"
)

func (c *DMCache) Gather(acc telegraf.Accumulator) error {
	return nil
}

func dmSetupStatus() ([]string, error) {
	return []string{}, nil
}
