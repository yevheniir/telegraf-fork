package all

import (
	_ "github.com/yevheniir/telegraf-fork/plugins/processors/converter"
	_ "github.com/yevheniir/telegraf-fork/plugins/processors/date"
	_ "github.com/yevheniir/telegraf-fork/plugins/processors/enum"
	_ "github.com/yevheniir/telegraf-fork/plugins/processors/override"
	_ "github.com/yevheniir/telegraf-fork/plugins/processors/parser"
	_ "github.com/yevheniir/telegraf-fork/plugins/processors/pivot"
	_ "github.com/yevheniir/telegraf-fork/plugins/processors/printer"
	_ "github.com/yevheniir/telegraf-fork/plugins/processors/regex"
	_ "github.com/yevheniir/telegraf-fork/plugins/processors/rename"
	_ "github.com/yevheniir/telegraf-fork/plugins/processors/strings"
	_ "github.com/yevheniir/telegraf-fork/plugins/processors/tag_limit"
	_ "github.com/yevheniir/telegraf-fork/plugins/processors/topk"
	_ "github.com/yevheniir/telegraf-fork/plugins/processors/unpivot"
)
