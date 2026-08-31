import re

with open('internal/webhook/handler.go', 'r') as f:
    content = f.read()

# The processAlert function contains the logic.
# We need to replace the section from fetching telemetry down to creating github issue.

old_logic = """	span.AddEvent("Fetching Telemetry")
	telemetry := k8s.FetchTelemetry(labels)

	log.Printf("New alert %s. Running Triage Minion...", alertName)
	span.AddEvent("Running Triage Minion")
	diagnosis := minion.RunTriage(ctx, alertName, labels, annotations, telemetry)

	if diagnosis == "IGNORED" {
		log.Printf("Triage Minion ignored alert %s. Halting.", alertName)
		span.AddEvent("Alert IGNORED by Triage Minion. Halting.")
		return
	}

	if labels["namespace"] == "apps" {
		serviceName := labels["service"]
		if serviceName == "" {
			serviceName = labels["app"]
		}
		endpointOrTopic := labels["endpoint"]
		if endpointOrTopic == "" {
			endpointOrTopic = labels["topic"]
		}
		statusCode := labels["status_code"]
		if statusCode == "" {
			statusCode = "UNKNOWN"
		}
		traceID := labels["trace_id"]
		if traceID == "" {
			traceID = annotations["trace_id"]
		}

		log.Printf("Routing incident to AppFixer (Service: %s)", serviceName)
		span.AddEvent("Routing to AppFixer")
		err := minion.AppFixer(serviceName, endpointOrTopic, statusCode, traceID, diagnosis)
		if err != nil {
			log.Printf("AppFixer encountered an error: %v", err)
			span.RecordError(err)
		}
		return // Do not create a GitHub issue or PR for App errors right now
	}"""

new_logic = """	if labels["namespace"] == "apps" {
		serviceName := labels["service"]
		if serviceName == "" {
			serviceName = labels["app"]
		}
		endpointOrTopic := labels["endpoint"]
		if endpointOrTopic == "" {
			endpointOrTopic = labels["topic"]
		}
		statusCode := labels["status_code"]
		if statusCode == "" {
			statusCode = "UNKNOWN"
		}
		traceID := labels["trace_id"]
		if traceID == "" {
			traceID = annotations["trace_id"]
		}

		// 1. Deduplicate against Postgres BEFORE calling Triage
		isDuplicate, err := minion.TrackAppIncident(serviceName, endpointOrTopic, statusCode, traceID)
		if err != nil {
			log.Printf("AppFixer tracking error: %v", err)
			span.RecordError(err)
		}
		if isDuplicate {
			span.AddEvent("App incident deduplicated by Postgres. Halting.")
			return
		}

		// 2. Fetch Telemetry and Run Triage for new incident
		span.AddEvent("Fetching Telemetry")
		telemetry := k8s.FetchTelemetry(labels)

		log.Printf("New app alert %s. Running Triage Minion...", alertName)
		span.AddEvent("Running Triage Minion")
		diagnosis := minion.RunTriage(ctx, alertName, labels, annotations, telemetry)

		state := "open"
		if diagnosis == "IGNORED" {
			log.Printf("Triage Minion ignored app alert %s. Saving state as ignored.", alertName)
			span.AddEvent("Alert IGNORED by Triage Minion.")
			state = "ignored"
		}

		// 3. Save new incident to database
		err = minion.SaveNewAppIncident(serviceName, endpointOrTopic, statusCode, traceID, state, diagnosis)
		if err != nil {
			log.Printf("AppFixer save error: %v", err)
			span.RecordError(err)
		}

		return // Halt for apps, no GitHub PRs for now
	}

	span.AddEvent("Fetching Telemetry")
	telemetry := k8s.FetchTelemetry(labels)

	log.Printf("New alert %s. Running Triage Minion...", alertName)
	span.AddEvent("Running Triage Minion")
	diagnosis := minion.RunTriage(ctx, alertName, labels, annotations, telemetry)

	if diagnosis == "IGNORED" {
		log.Printf("Triage Minion ignored alert %s. Halting.", alertName)
		span.AddEvent("Alert IGNORED by Triage Minion. Halting.")
		return
	}"""

content = content.replace(old_logic, new_logic)

with open('internal/webhook/handler.go', 'w') as f:
    f.write(content)
