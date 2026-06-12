import json

with open('deploy/grafana/provisioning/dashboards/go-metrics.json') as f:
    dash = json.load(f)

# Update Templating (Variables)
dash['templating'] = {
  "list": [
    {
      "allValue": ".*",
      "current": {"selected": True, "text": [ "All" ], "value": [ "$__all" ]},
      "datasource": {"type": "prometheus", "uid": "PBFA97CFB590B2093"},
      "definition": "label_values(go_goroutine_count, service_name)",
      "hide": 0,
      "includeAll": True,
      "multi": True,
      "name": "service_name",
      "options": [],
      "query": "label_values(go_goroutine_count, service_name)",
      "refresh": 1,
      "regex": "",
      "skipUrlSync": False,
      "sort": 1,
      "type": "query"
    }
  ]
}

# Define new panels based on OpenTelemetry Go Runtime Metrics
new_panels = [
  {
    "title": "Running Goroutines",
    "type": "timeseries",
    "gridPos": {"h": 8, "w": 12, "x": 0, "y": 0},
    "targets": [
      {
        "datasource": {"type": "prometheus", "uid": "PBFA97CFB590B2093"},
        "expr": 'go_goroutine_count{service_name=~"$service_name"}',
        "legendFormat": "{{service_name}}"
      }
    ]
  },
  {
    "title": "Memory Used Bytes",
    "type": "timeseries",
    "gridPos": {"h": 8, "w": 12, "x": 12, "y": 0},
    "targets": [
      {
        "datasource": {"type": "prometheus", "uid": "PBFA97CFB590B2093"},
        "expr": 'go_memory_used_bytes{service_name=~"$service_name"}',
        "legendFormat": "{{service_name}}"
      }
    ],
    "fieldConfig": {"defaults": {"unit": "bytes"}}
  },
  {
    "title": "Memory GC Goal Bytes",
    "type": "timeseries",
    "gridPos": {"h": 8, "w": 12, "x": 0, "y": 8},
    "targets": [
      {
        "datasource": {"type": "prometheus", "uid": "PBFA97CFB590B2093"},
        "expr": 'go_memory_gc_goal_bytes{service_name=~"$service_name"}',
        "legendFormat": "{{service_name}}"
      }
    ],
    "fieldConfig": {"defaults": {"unit": "bytes"}}
  },
  {
    "title": "Allocated Memory Rate",
    "type": "timeseries",
    "gridPos": {"h": 8, "w": 12, "x": 12, "y": 8},
    "targets": [
      {
        "datasource": {"type": "prometheus", "uid": "PBFA97CFB590B2093"},
        "expr": 'rate(go_memory_allocated_bytes_total{service_name=~"$service_name"}[1m])',
        "legendFormat": "{{service_name}}"
      }
    ],
    "fieldConfig": {"defaults": {"unit": "Bps"}}
  },
  {
    "title": "Allocation Count Rate (Objects/sec)",
    "type": "timeseries",
    "gridPos": {"h": 8, "w": 12, "x": 0, "y": 16},
    "targets": [
      {
        "datasource": {"type": "prometheus", "uid": "PBFA97CFB590B2093"},
        "expr": 'rate(go_memory_allocations_total{service_name=~"$service_name"}[1m])',
        "legendFormat": "{{service_name}}"
      }
    ],
    "fieldConfig": {"defaults": {"unit": "short"}}
  },
  {
    "title": "Processor Limit (GOMAXPROCS)",
    "type": "timeseries",
    "gridPos": {"h": 8, "w": 12, "x": 12, "y": 16},
    "targets": [
      {
        "datasource": {"type": "prometheus", "uid": "PBFA97CFB590B2093"},
        "expr": 'go_processor_limit{service_name=~"$service_name"}',
        "legendFormat": "{{service_name}}"
      }
    ]
  }
]

# Set IDs incrementally to be safe
for i, p in enumerate(new_panels):
    p['id'] = i + 1

dash['panels'] = new_panels

with open('deploy/grafana/provisioning/dashboards/go-metrics.json', 'w') as f:
    json.dump(dash, f, indent=2)

print('Successfully completely replaced go-metrics dashboard with OpenTelemetry compatible queries.')
