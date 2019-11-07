// +build !linux,!freebsd

package zfs

import (
	"github.com/yevheniir/telegraf-fork"
	"github.com/yevheniir/telegraf-fork/plugins/inputs"
)

func (z *Zfs) Gather(acc telegraf.Accumulator) error {
	return nil
}

func init() {
	inputs.Add("zfs", func() telegraf.Input {
		return &Zfs{}
	})
}
