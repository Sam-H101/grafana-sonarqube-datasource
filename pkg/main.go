package main

import (
	"os"

	"github.com/Sam-H101/grafana-sonarqube-datasource/pkg/plugin"
	"github.com/grafana/grafana-plugin-sdk-go/backend/datasource"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
)

func main() {
	// Manage creates one plugin.Datasource per configured data source and
	// replaces it when its settings change. The call blocks until Grafana
	// stops the plugin.
	if err := datasource.Manage("smartquinoa1504-sonarqube-datasource", plugin.NewDatasource, datasource.ManageOpts{}); err != nil {
		log.DefaultLogger.Error(err.Error())
		os.Exit(1)
	}
}
