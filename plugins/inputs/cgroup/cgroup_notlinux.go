// +build !linux

package cgroup

import (
	"github.com/yevheniir/telegraf-fork"
)

func (g *CGroup) Gather(acc telegraf.Accumulator) error {
	return nil
}
