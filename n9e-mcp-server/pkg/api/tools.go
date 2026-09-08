package api

import (
	"github.com/n9e/n9e-mcp-server/pkg/client"
	"github.com/n9e/n9e-mcp-server/pkg/toolset"
)

// DefaultToolsetGroup registers the toolsets this repo actually ships.
func DefaultToolsetGroup(getClient client.GetClientFunc, readOnly bool) *toolset.ToolsetGroup {
	group := toolset.NewToolsetGroup(readOnly)
	RegisterAlertsToolset(group, getClient)
	RegisterTargetsToolset(group, getClient)
	RegisterDatasourceToolset(group, getClient)
	RegisterBusiGroupsToolset(group, getClient)
	RegisterMetricsToolset(group, getClient)
	RegisterLogsToolset(group, getClient)
	return group
}
