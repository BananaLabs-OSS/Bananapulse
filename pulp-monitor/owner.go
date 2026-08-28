package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// owner applies each command to an isolated copy, persists the durable event,
// then publishes it. A storage error can therefore never leak a changed read
// projection. Re-opening the same store replays the exact same command log.
type owner struct {
	mu    sync.Mutex
	state *monitorState
	store EventStore
}

func openOwner(ctx context.Context, store EventStore) (*owner, error) {
	if err := store.Migrate(ctx); err != nil {
		return nil, fmt.Errorf("migrate monitor store: %w", err)
	}
	events, err := store.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("load monitor events: %w", err)
	}
	state := newMonitorState()
	for _, command := range events {
		if _, err := state.apply(command); err != nil {
			return nil, fmt.Errorf("replay command %q: %w", command.ID, err)
		}
	}
	return &owner{state: state, store: store}, nil
}

func (o *owner) apply(ctx context.Context, command Command) (CommandResult, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if result, ok := o.state.commands[command.ID]; ok {
		result.Deduped = true
		return result, nil
	}
	next := o.state.clone()
	result, err := next.apply(command)
	if err != nil {
		return CommandResult{}, err
	}
	if err := o.store.Append(ctx, command, result); err != nil {
		return CommandResult{}, fmt.Errorf("persist monitor command %q: %w", command.ID, err)
	}
	o.state = next
	return result, nil
}

func (o *owner) projection(atUnix int64) Projection {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.state.projection(atUnix)
}

func (o *owner) query(query Query) (Projection, error) {
	if err := validVersion(query.Version); err != nil {
		return Projection{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.state.query(query)
}

type monitorState struct {
	revision     uint64
	commands     map[string]CommandResult
	components   map[string]Component
	sources      map[string]Source
	mappings     map[string]SourceTargetMapping
	observations []Observation
	incidents    map[string]Incident
	updates      []IncidentUpdate
	maintenance  map[string]Maintenance
}

func newMonitorState() *monitorState {
	return &monitorState{commands: map[string]CommandResult{}, components: map[string]Component{}, sources: map[string]Source{}, mappings: map[string]SourceTargetMapping{}, incidents: map[string]Incident{}, maintenance: map[string]Maintenance{}}
}

func (s *monitorState) clone() *monitorState {
	next := newMonitorState()
	next.revision = s.revision
	for k, v := range s.commands {
		next.commands[k] = v
	}
	for k, v := range s.components {
		v.Uptime90D = append([]UptimeDay(nil), v.Uptime90D...)
		next.components[k] = v
	}
	for k, v := range s.sources {
		next.sources[k] = v
	}
	for k, v := range s.mappings {
		next.mappings[k] = v
	}
	next.observations = append([]Observation(nil), s.observations...)
	for k, v := range s.incidents {
		v.Affects = append([]string(nil), v.Affects...)
		next.incidents[k] = v
	}
	next.updates = append([]IncidentUpdate(nil), s.updates...)
	for k, v := range s.maintenance {
		v.Affects = append([]string(nil), v.Affects...)
		next.maintenance[k] = v
	}
	return next
}

func (s *monitorState) apply(command Command) (CommandResult, error) {
	if err := validVersion(command.Version); err != nil {
		return CommandResult{}, err
	}
	if err := requireID("command id", command.ID); err != nil {
		return CommandResult{}, err
	}
	if _, exists := s.commands[command.ID]; exists {
		result := s.commands[command.ID]
		result.Deduped = true
		return result, nil
	}
	beforeDomain := s.domainSnapshot()
	result := CommandResult{Version: ContractVersion, CommandID: command.ID}
	var err error
	switch command.Kind {
	case UpsertComponent:
		err = s.upsertComponent(command.Component, command.AtUnix)
	case EditComponent:
		err = s.editComponent(command.ComponentPatch, command.AtUnix)
	case ArchiveComponent:
		result.ComponentIDs, err = s.setComponentArchived(command.ComponentID, true, command.ArchiveBatchID, command.AtUnix)
	case RestoreComponent:
		result.ComponentIDs, err = s.setComponentArchived(command.ComponentID, false, command.ArchiveBatchID, command.AtUnix)
	case UpsertSource:
		err = s.upsertSource(command.Source, command.AtUnix)
	case EditSource:
		err = s.editSource(command.SourcePatch, command.AtUnix)
	case RevokeSource:
		err = s.setSourceRevoked(command.SourceID, true, command.AtUnix)
	case RestoreSource:
		err = s.setSourceRevoked(command.SourceID, false, command.AtUnix)
	case MapSourceTarget:
		result.MappingID, err = s.mapSourceTarget(command.Mapping)
	case UnmapSourceTarget:
		err = s.unmapSourceTarget(command.MappingID)
	case AppendObservation:
		err = s.appendObservation(command.Observation)
	case IngestObservation:
		var componentID string
		componentID, err = s.ingestObservation(command.Ingest, command.ComponentPatch, command.AtUnix)
		if err == nil {
			result.ComponentIDs = []string{componentID}
		}
	case OpenIncident:
		err = s.openIncident(command.Incident, command.AtUnix, !command.ImportMode)
	case EditIncident:
		err = s.editIncident(command.IncidentPatch, command.AtUnix)
	case UpdateIncident:
		err = s.updateIncident(command.Update, command.ImportMode)
	case ResolveIncident:
		err = s.resolveIncident(command.Incident, command.AtUnix)
	case DeleteIncident:
		err = s.deleteIncident(command.IncidentID)
	case ScheduleMaintenance:
		err = s.scheduleMaintenance(command.Maintenance, command.AtUnix)
	case EditMaintenance:
		err = s.editMaintenance(command.MaintenancePatch)
	case CancelMaintenance:
		err = s.cancelMaintenance(command.MaintenanceID, command.AtUnix)
	case DeleteMaintenance:
		err = s.deleteMaintenance(command.MaintenanceID)
	case SweepReconcile:
		result.Sweep, err = s.sweep(command.ID, command.AtUnix)
	default:
		err = fmt.Errorf("unknown monitor command %q", command.Kind)
	}
	if err != nil {
		return CommandResult{}, err
	}
	s.revision++
	result.Revision = s.revision
	// Quorum reconciliation belongs to the durable state machine. It is replayed
	// after every command, including config changes that can make reads stale.
	if !command.ImportMode && command.Kind != OpenIncident && command.Kind != EditIncident && command.Kind != UpdateIncident && command.Kind != ResolveIncident && command.Kind != DeleteIncident && command.Kind != SweepReconcile {
		s.reconcileAll(command.AtUnix)
	}
	if command.Kind == IngestObservation && len(result.ComponentIDs) == 1 {
		request := command.Ingest
		evaluation := s.evaluate(result.ComponentIDs[0], command.AtUnix)
		result.Evaluation = ingestEvaluation(request.ObservationID, evaluation)
	}
	if !command.ImportMode {
		result.Transitions = diffDomain(beforeDomain, s.domainSnapshot(), command.ID, command.AtUnix)
	}
	s.commands[command.ID] = result
	return result, nil
}

func (s *monitorState) upsertComponent(value *Component, now int64) error {
	if value == nil {
		return fmt.Errorf("component is required")
	}
	if err := requireID("component id", value.ID); err != nil {
		return err
	}
	if value.Name == "" {
		return fmt.Errorf("component name is required")
	}
	if value.ParentID == value.ID {
		return fmt.Errorf("component %q cannot parent itself", value.ID)
	}
	if value.ParentID != "" {
		parentValue, ok := s.components[value.ParentID]
		if !ok {
			return fmt.Errorf("parent component %q not found", value.ParentID)
		}
		if parentValue.Archived {
			return fmt.Errorf("parent component %q is archived", value.ParentID)
		}
		for parent := value.ParentID; parent != ""; parent = s.components[parent].ParentID {
			if parent == value.ID {
				return fmt.Errorf("component tree cycle at %q", value.ID)
			}
		}
	}
	if value.Kind == "" {
		value.Kind = "service"
	}
	if value.FallbackStatus == "" {
		value.FallbackStatus = StatusOperational
	}
	switch value.FallbackStatus {
	case "ok":
		value.FallbackStatus = StatusOperational
	case "deg":
		value.FallbackStatus = StatusDegraded
	case "out":
		value.FallbackStatus = StatusOutage
	}
	if !validStatus(value.FallbackStatus) {
		return fmt.Errorf("invalid fallback status %q", value.FallbackStatus)
	}
	if existing, ok := s.components[value.ID]; ok {
		if value.CreatedAtUnix == 0 {
			value.CreatedAtUnix = existing.CreatedAtUnix
		}
		if value.Archived != existing.Archived || value.ArchivedAtUnix != existing.ArchivedAtUnix || value.ArchiveBatchID != existing.ArchiveBatchID {
			return fmt.Errorf("component archive state must use archive or restore command")
		}
		if !value.LaunchedSet {
			value.Launched = existing.Launched
		}
	} else {
		if value.CreatedAtUnix == 0 {
			value.CreatedAtUnix = now
		}
		if value.Archived || value.ArchivedAtUnix != 0 || value.ArchiveBatchID != "" {
			return fmt.Errorf("new component cannot start archived")
		}
		if !value.LaunchedSet {
			value.Launched = true
		}
	}
	value.LaunchedSet = true
	value.Uptime90D = append([]UptimeDay(nil), value.Uptime90D...)
	s.components[value.ID] = *value
	return nil
}

func (s *monitorState) editComponent(value *ComponentPatch, now int64) error {
	if value == nil {
		return fmt.Errorf("component patch is required")
	}
	if err := requireID("component id", value.ID); err != nil {
		return err
	}
	component, ok := s.components[value.ID]
	if !ok {
		return fmt.Errorf("component %q not found", value.ID)
	}
	if value.ParentID != nil {
		component.ParentID = *value.ParentID
	}
	if value.Name != nil {
		component.Name = *value.Name
	}
	if value.Kind != nil {
		component.Kind = *value.Kind
	}
	if value.Tag != nil {
		component.Tag = *value.Tag
	}
	if value.Brand != nil {
		component.Brand = *value.Brand
	}
	if value.Domain != nil {
		component.Domain = *value.Domain
	}
	if value.Uptime90D != nil {
		component.Uptime90D = append([]UptimeDay(nil), (*value.Uptime90D)...)
	}
	if value.SortOrder != nil {
		component.SortOrder = *value.SortOrder
	}
	if value.FallbackStatus != nil {
		component.FallbackStatus = *value.FallbackStatus
	}
	if value.Critical != nil {
		component.Critical = *value.Critical
	}
	if value.Launched != nil {
		component.Launched = *value.Launched
		component.LaunchedSet = true
	}
	return s.upsertComponent(&component, now)
}

func (s *monitorState) setComponentArchived(id string, archived bool, batchID string, now int64) ([]string, error) {
	if err := requireID("component id", id); err != nil {
		return nil, err
	}
	value, ok := s.components[id]
	if !ok {
		return nil, fmt.Errorf("component %q not found", id)
	}
	ids := s.descendantIDs(id, true)
	if archived {
		if value.Archived {
			return []string{}, nil
		}
		if now <= 0 {
			return nil, fmt.Errorf("archive time is required")
		}
		if batchID == "" {
			batchID = id + "/" + fmt.Sprint(now)
		}
		for _, componentID := range ids {
			component := s.components[componentID]
			if component.Archived {
				continue
			}
			evaluation := s.evaluate(componentID, now)
			if evaluation.State == "declared" || s.hasOpenIncident(componentID) {
				return nil, fmt.Errorf("cannot archive component %q with a live declared outage", componentID)
			}
		}
		changed := ids[:0]
		for _, componentID := range ids {
			component := s.components[componentID]
			if component.Archived {
				continue
			}
			component.Archived = true
			component.ArchivedAtUnix = now
			component.ArchiveBatchID = batchID
			s.components[componentID] = component
			changed = append(changed, componentID)
		}
		return append([]string(nil), changed...), nil
	}
	if !value.Archived {
		return []string{}, nil
	}
	if batchID == "" {
		batchID = value.ArchiveBatchID
	}
	if value.ParentID != "" && s.components[value.ParentID].Archived {
		return nil, fmt.Errorf("cannot restore component %q under archived parent %q", id, value.ParentID)
	}
	changed := ids[:0]
	for _, componentID := range ids {
		component := s.components[componentID]
		if !component.Archived || component.ArchiveBatchID != batchID {
			continue
		}
		component.Archived = false
		component.ArchivedAtUnix = 0
		component.ArchiveBatchID = ""
		s.components[componentID] = component
		changed = append(changed, componentID)
	}
	return append([]string(nil), changed...), nil
}

func (s *monitorState) upsertSource(value *Source, now int64) error {
	if value == nil {
		return fmt.Errorf("source is required")
	}
	if err := requireID("source id", value.ID); err != nil {
		return err
	}
	if value.Name == "" {
		return fmt.Errorf("source name is required")
	}
	if value.Weight <= 0 {
		return fmt.Errorf("source weight must be positive")
	}
	if value.Kind == "" {
		value.Kind = SourcePush
	}
	value.Kind = SourceKind(strings.ToLower(strings.TrimSpace(string(value.Kind))))
	if !validSourceKind(value.Kind) {
		return fmt.Errorf("invalid source kind %q", value.Kind)
	}
	if value.DefaultTTL != nil && *value.DefaultTTL < 0 {
		return fmt.Errorf("source default ttl cannot be negative")
	}
	if existing, ok := s.sources[value.ID]; ok {
		if value.CreatedAtUnix == 0 {
			value.CreatedAtUnix = existing.CreatedAtUnix
		}
		if value.Revoked != existing.Revoked || value.RevokedAtUnix != existing.RevokedAtUnix {
			return fmt.Errorf("source revoke state must use revoke or restore command")
		}
	} else {
		if value.CreatedAtUnix == 0 {
			value.CreatedAtUnix = now
		}
		if value.Revoked || value.RevokedAtUnix != 0 {
			return fmt.Errorf("new source cannot start revoked")
		}
	}
	s.sources[value.ID] = *value
	return nil
}

func (s *monitorState) editSource(value *SourcePatch, now int64) error {
	if value == nil {
		return fmt.Errorf("source patch is required")
	}
	if err := requireID("source id", value.ID); err != nil {
		return err
	}
	source, ok := s.sources[value.ID]
	if !ok {
		return fmt.Errorf("source %q not found", value.ID)
	}
	if value.Name != nil {
		source.Name = *value.Name
	}
	if value.Weight != nil {
		source.Weight = *value.Weight
	}
	if value.Kind != nil {
		source.Kind = *value.Kind
	}
	if value.Trusted != nil {
		source.Trusted = *value.Trusted
	}
	if value.DirectTargets != nil {
		source.DirectTargets = *value.DirectTargets
	}
	if value.DefaultTTLSet {
		source.DefaultTTL = value.DefaultTTL
	}
	return s.upsertSource(&source, now)
}

func (s *monitorState) setSourceRevoked(id string, revoked bool, now int64) error {
	if err := requireID("source id", id); err != nil {
		return err
	}
	value, ok := s.sources[id]
	if !ok {
		return fmt.Errorf("source %q not found", id)
	}
	if revoked {
		if now <= 0 {
			return fmt.Errorf("source revoke time is required")
		}
		value.Revoked = true
		value.RevokedAtUnix = now
	} else {
		value.Revoked = false
		value.RevokedAtUnix = 0
	}
	s.sources[id] = value
	return nil
}

func (s *monitorState) mapSourceTarget(value *SourceTargetMapping) (string, error) {
	if value == nil {
		return "", fmt.Errorf("mapping is required")
	}
	if err := requireID("mapping id", value.ID); err != nil {
		return "", err
	}
	if err := requireID("mapping source", value.SourceID); err != nil {
		return "", err
	}
	source, ok := s.sources[value.SourceID]
	if !ok {
		return "", fmt.Errorf("source %q not found", value.SourceID)
	}
	if source.Revoked {
		return "", fmt.Errorf("source %q is revoked", value.SourceID)
	}
	if value.RawLabel == "" {
		return "", fmt.Errorf("mapping raw label is required")
	}
	component, ok := s.components[value.ComponentID]
	if !ok {
		return "", fmt.Errorf("component %q not found", value.ComponentID)
	}
	if component.Archived {
		return "", fmt.Errorf("component %q is archived", value.ComponentID)
	}
	if existing, ok := s.mappings[value.ID]; ok && (existing.SourceID != value.SourceID || existing.RawLabel != value.RawLabel) {
		return "", fmt.Errorf("mapping %q identity cannot change", value.ID)
	}
	for id, mapping := range s.mappings {
		if id != value.ID && mapping.SourceID == value.SourceID && mapping.RawLabel == value.RawLabel {
			mapping.ComponentID = value.ComponentID
			s.mappings[id] = mapping
			return id, nil
		}
	}
	s.mappings[value.ID] = *value
	return value.ID, nil
}

func (s *monitorState) unmapSourceTarget(id string) error {
	if err := requireID("mapping id", id); err != nil {
		return err
	}
	if _, ok := s.mappings[id]; !ok {
		return fmt.Errorf("mapping %q not found", id)
	}
	delete(s.mappings, id)
	return nil
}

func (s *monitorState) appendObservation(value *Observation) error {
	if value == nil {
		return fmt.Errorf("observation is required")
	}
	if err := requireID("observation id", value.ID); err != nil {
		return err
	}
	source, sourceExists := s.sources[value.SourceID]
	if !sourceExists {
		return fmt.Errorf("source %q not found", value.SourceID)
	}
	if source.Revoked {
		return fmt.Errorf("source %q is revoked", value.SourceID)
	}
	component, componentExists := s.components[value.ComponentID]
	if !componentExists {
		return fmt.Errorf("component %q not found", value.ComponentID)
	}
	if component.Archived {
		return fmt.Errorf("component %q is archived", value.ComponentID)
	}
	if !validSignal(value.Signal) {
		return fmt.Errorf("invalid observation signal %q", value.Signal)
	}
	if value.ObservedAtUnix <= 0 {
		return fmt.Errorf("observation time is required")
	}
	if value.ExpiresAtUnix != 0 && value.ExpiresAtUnix < value.ObservedAtUnix {
		return fmt.Errorf("observation expiry precedes observation")
	}
	for _, existing := range s.observations {
		if existing.ID == value.ID {
			return fmt.Errorf("observation %q already exists", value.ID)
		}
	}
	if value.ExpiresAtUnix == 0 && source.DefaultTTL != nil && *source.DefaultTTL > 0 {
		value.ExpiresAtUnix = value.ObservedAtUnix + *source.DefaultTTL
	}
	s.observations = append(s.observations, *value)
	return nil
}

func (s *monitorState) ingestObservation(value *IngestRequest, componentPatch *ComponentPatch, now int64) (string, error) {
	if value == nil {
		return "", fmt.Errorf("ingest request is required")
	}
	if err := requireID("observation id", value.ObservationID); err != nil {
		return "", err
	}
	if err := requireID("source id", value.SourceID); err != nil {
		return "", err
	}
	source, ok := s.sources[value.SourceID]
	if !ok {
		return "", fmt.Errorf("source %q not found", value.SourceID)
	}
	if source.Revoked {
		return "", fmt.Errorf("source %q is revoked", value.SourceID)
	}
	if (value.RawLabel == "") == (value.ComponentID == "") {
		return "", fmt.Errorf("exactly one of raw label or canonical component id is required")
	}
	componentID := value.ComponentID
	if componentID == "" {
		for _, mapping := range s.mappings {
			if mapping.SourceID == value.SourceID && mapping.RawLabel == value.RawLabel {
				if componentID != "" && componentID != mapping.ComponentID {
					return "", fmt.Errorf("ambiguous source target mapping")
				}
				componentID = mapping.ComponentID
			}
		}
		if componentID == "" {
			return "", fmt.Errorf("source target %q is not mapped", value.RawLabel)
		}
	} else {
		if !source.DirectTargets {
			return "", fmt.Errorf("source %q does not permit direct targets", value.SourceID)
		}
		component, ok := s.components[componentID]
		if !ok || component.Archived {
			return "", fmt.Errorf("component %q not found", componentID)
		}
	}
	if componentPatch != nil {
		if componentPatch.ID != componentID {
			return "", fmt.Errorf("ingest component patch must target resolved component %q", componentID)
		}
		if componentPatch.Uptime90D == nil || componentPatch.ParentID != nil || componentPatch.Name != nil || componentPatch.Kind != nil || componentPatch.Tag != nil || componentPatch.Brand != nil || componentPatch.Domain != nil || componentPatch.SortOrder != nil || componentPatch.FallbackStatus != nil || componentPatch.Critical != nil || componentPatch.Launched != nil {
			return "", fmt.Errorf("ingest component patch may only update uptime_90d")
		}
		if err := s.editComponent(componentPatch, now); err != nil {
			return "", err
		}
	}
	observedAt := value.ObservedAtUnix
	if observedAt == 0 {
		observedAt = now
	}
	observation := &Observation{
		ID:             value.ObservationID,
		SourceID:       value.SourceID,
		ComponentID:    componentID,
		Signal:         value.Signal,
		Detail:         value.Detail,
		ObservedAtUnix: observedAt,
		ExpiresAtUnix:  value.ExpiresAtUnix,
	}
	if err := s.appendObservation(observation); err != nil {
		return "", err
	}
	return componentID, nil
}

func (s *monitorState) openIncident(value *Incident, now int64, addOpenedUpdate bool) error {
	if value == nil {
		return fmt.Errorf("incident is required")
	}
	if err := requireID("incident id", value.ID); err != nil {
		return err
	}
	if value.Title == "" || value.Summary == "" {
		return fmt.Errorf("incident title and summary are required")
	}
	if value.Severity == "" {
		value.Severity = "minor"
	}
	if !validIncidentStatus(value.Status) && value.Status != "" {
		return fmt.Errorf("invalid incident status %q", value.Status)
	}
	if !validIncidentSeverity(value.Severity) {
		return fmt.Errorf("invalid incident severity %q", value.Severity)
	}
	if len(value.Affects) == 0 {
		return fmt.Errorf("incident affects is required")
	}
	for _, id := range value.Affects {
		component, ok := s.components[id]
		if !ok || component.Archived {
			return fmt.Errorf("incident component %q not found", id)
		}
		if !isLeafKind(component.Kind) {
			return fmt.Errorf("incident component %q is not a leaf", id)
		}
	}
	if value.Status == "" {
		value.Status = "investigating"
	}
	if value.StartedAtUnix == 0 {
		value.StartedAtUnix = now
	}
	if value.StartedAtUnix <= 0 {
		return fmt.Errorf("incident start time is required")
	}
	if value.CreatedAtUnix == 0 {
		value.CreatedAtUnix = now
	}
	if _, exists := s.incidents[value.ID]; exists {
		return fmt.Errorf("incident %q already exists", value.ID)
	}
	value.Affects = canonicalStrings(value.Affects)
	s.incidents[value.ID] = *value
	if addOpenedUpdate {
		s.updates = append(s.updates, IncidentUpdate{ID: value.ID + "/opened", IncidentID: value.ID, AtUnix: value.StartedAtUnix, Label: value.Status, Body: value.Summary, Author: "engine"})
	}
	return nil
}

func (s *monitorState) editIncident(value *IncidentPatch, now int64) error {
	if value == nil {
		return fmt.Errorf("incident patch is required")
	}
	if err := requireID("incident id", value.ID); err != nil {
		return err
	}
	incident, ok := s.incidents[value.ID]
	if !ok {
		return fmt.Errorf("incident %q not found", value.ID)
	}
	if value.Title != nil {
		if *value.Title == "" {
			return fmt.Errorf("incident title cannot be empty")
		}
		incident.Title = *value.Title
	}
	if value.Summary != nil {
		if *value.Summary == "" {
			return fmt.Errorf("incident summary cannot be empty")
		}
		incident.Summary = *value.Summary
	}
	if value.Severity != nil {
		if !validIncidentSeverity(*value.Severity) {
			return fmt.Errorf("invalid incident severity %q", *value.Severity)
		}
		incident.Severity = *value.Severity
	}
	if value.Affects != nil {
		if err := s.validateLeafReferences("incident", value.Affects); err != nil {
			return err
		}
		incident.Affects = canonicalStrings(value.Affects)
	}
	if value.Status != nil {
		if !validIncidentStatus(*value.Status) {
			return fmt.Errorf("invalid incident status %q", *value.Status)
		}
		if *value.Status == "resolved" {
			return fmt.Errorf("resolve incident with resolve_incident")
		}
		if incident.ResolvedAtUnix != 0 {
			return fmt.Errorf("incident %q is resolved", value.ID)
		}
		incident.Status = *value.Status
	}
	s.incidents[incident.ID] = incident
	if value.Note != "" {
		at := value.AtUnix
		if at == 0 {
			at = now
		}
		if at <= 0 {
			return fmt.Errorf("incident note time is required")
		}
		updateID := incident.ID + "/edit/" + fmt.Sprint(at)
		if s.hasUpdate(updateID) {
			return fmt.Errorf("incident update %q already exists", updateID)
		}
		s.updates = append(s.updates, IncidentUpdate{ID: updateID, IncidentID: incident.ID, AtUnix: at, Label: incident.Status, Body: value.Note, Author: value.Author})
	}
	return nil
}

func (s *monitorState) updateIncident(value *IncidentUpdate, importMode bool) error {
	if value == nil {
		return fmt.Errorf("incident update is required")
	}
	if err := requireID("incident update id", value.ID); err != nil {
		return err
	}
	incident, ok := s.incidents[value.IncidentID]
	if !ok {
		return fmt.Errorf("incident %q not found", value.IncidentID)
	}
	if incident.ResolvedAtUnix != 0 && !importMode {
		return fmt.Errorf("incident %q is resolved", value.IncidentID)
	}
	if value.AtUnix <= 0 || value.Label == "" || value.Body == "" {
		return fmt.Errorf("incident update time, label, and body are required")
	}
	if s.hasUpdate(value.ID) {
		return fmt.Errorf("incident update %q already exists", value.ID)
	}
	s.updates = append(s.updates, *value)
	return nil
}

func (s *monitorState) resolveIncident(value *Incident, now int64) error {
	if value == nil {
		return fmt.Errorf("incident is required")
	}
	incident, ok := s.incidents[value.ID]
	if !ok {
		return fmt.Errorf("incident %q not found", value.ID)
	}
	if incident.ResolvedAtUnix != 0 {
		return nil
	}
	if now == 0 {
		now = value.ResolvedAtUnix
	}
	if now == 0 {
		return fmt.Errorf("resolution time is required")
	}
	incident.Status = "resolved"
	incident.ResolvedAtUnix = now
	s.incidents[incident.ID] = incident
	s.updates = append(s.updates, IncidentUpdate{ID: incident.ID + "/resolved/" + fmt.Sprint(now), IncidentID: incident.ID, AtUnix: now, Label: "resolved", Body: "Monitoring reports recovery.", Author: "engine"})
	return nil
}

func (s *monitorState) deleteIncident(id string) error {
	if err := requireID("incident id", id); err != nil {
		return err
	}
	if _, ok := s.incidents[id]; !ok {
		return fmt.Errorf("incident %q not found", id)
	}
	delete(s.incidents, id)
	filtered := s.updates[:0]
	for _, update := range s.updates {
		if update.IncidentID != id {
			filtered = append(filtered, update)
		}
	}
	s.updates = filtered
	return nil
}

func (s *monitorState) scheduleMaintenance(value *Maintenance, now int64) error {
	if value == nil {
		return fmt.Errorf("maintenance is required")
	}
	if err := requireID("maintenance id", value.ID); err != nil {
		return err
	}
	if value.Title == "" || value.ScheduledStartUnix <= 0 || value.ScheduledEndUnix <= value.ScheduledStartUnix {
		return fmt.Errorf("maintenance title and valid schedule are required")
	}
	if err := s.validateLeafReferences("maintenance", value.Affects); err != nil {
		return err
	}
	if value.Kind == "" {
		value.Kind = "scheduled"
	}
	value.Affects = canonicalStrings(value.Affects)
	if len(value.Affects) == 0 {
		return fmt.Errorf("maintenance affects is required")
	}
	if value.CreatedAtUnix == 0 {
		value.CreatedAtUnix = now
	}
	if _, exists := s.maintenance[value.ID]; exists {
		return fmt.Errorf("maintenance %q already exists", value.ID)
	}
	s.maintenance[value.ID] = *value
	return nil
}

func (s *monitorState) editMaintenance(value *MaintenancePatch) error {
	if value == nil {
		return fmt.Errorf("maintenance patch is required")
	}
	if err := requireID("maintenance id", value.ID); err != nil {
		return err
	}
	maintenance, ok := s.maintenance[value.ID]
	if !ok {
		return fmt.Errorf("maintenance %q not found", value.ID)
	}
	if maintenance.Cancelled {
		return fmt.Errorf("maintenance %q is cancelled", value.ID)
	}
	if value.Title != nil {
		if *value.Title == "" {
			return fmt.Errorf("maintenance title cannot be empty")
		}
		maintenance.Title = *value.Title
	}
	if value.Summary != nil {
		maintenance.Summary = *value.Summary
	}
	if value.Kind != nil {
		if *value.Kind != "scheduled" && *value.Kind != "emergency" {
			return fmt.Errorf("invalid maintenance kind %q", *value.Kind)
		}
		maintenance.Kind = *value.Kind
	}
	if value.ScheduledStartUnix != nil {
		maintenance.ScheduledStartUnix = *value.ScheduledStartUnix
	}
	if value.ScheduledEndUnix != nil {
		maintenance.ScheduledEndUnix = *value.ScheduledEndUnix
	}
	if maintenance.ScheduledStartUnix <= 0 || maintenance.ScheduledEndUnix <= maintenance.ScheduledStartUnix {
		return fmt.Errorf("maintenance title and valid schedule are required")
	}
	if value.Affects != nil {
		if err := s.validateLeafReferences("maintenance", value.Affects); err != nil {
			return err
		}
		maintenance.Affects = canonicalStrings(value.Affects)
	}
	s.maintenance[value.ID] = maintenance
	return nil
}

func (s *monitorState) cancelMaintenance(id string, now int64) error {
	if err := requireID("maintenance id", id); err != nil {
		return err
	}
	value, ok := s.maintenance[id]
	if !ok {
		return fmt.Errorf("maintenance %q not found", id)
	}
	value.Cancelled = true
	value.CancelledAtUnix = now
	s.maintenance[id] = value
	return nil
}

func (s *monitorState) deleteMaintenance(id string) error {
	if err := requireID("maintenance id", id); err != nil {
		return err
	}
	if _, ok := s.maintenance[id]; !ok {
		return fmt.Errorf("maintenance %q not found", id)
	}
	delete(s.maintenance, id)
	return nil
}

func (s *monitorState) reconcileAll(now int64) {
	if now == 0 {
		return
	}
	ids := make([]string, 0, len(s.components))
	for id := range s.components {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if s.components[id].Archived {
			continue
		}
		s.reconcile(id, now)
	}
}

func (s *monitorState) sweep(commandID string, now int64) (*SweepResult, error) {
	if now <= 0 {
		return nil, fmt.Errorf("sweep time is required")
	}
	before := s.domainSnapshot()
	s.reconcileAll(now)
	after := s.domainSnapshot()
	result := &SweepResult{AtUnix: now}
	ids := make([]string, 0, len(s.components))
	for id, component := range s.components {
		if !component.Archived {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		evaluation := s.evaluate(id, now)
		result.Components++
		switch evaluation.State {
		case "declared":
			result.Declared++
		case "watch":
			result.Watch++
		}
		if evaluation.ReducedCoverage {
			result.ReducedCoverage++
		}
	}
	for _, transition := range diffDomain(before, after, commandID, now) {
		switch transition.Kind {
		case "incident.opened", "incident.updated", "incident.resolved":
			result.Transitions = append(result.Transitions, ReconcileTransition{
				ComponentID: transition.ComponentID,
				IncidentID:  transition.EntityID,
				State:       transition.Kind,
				Severity:    transition.Severity,
			})
		}
	}
	return result, nil
}

func (s *monitorState) reconcile(componentID string, now int64) {
	evaluation := s.evaluate(componentID, now)
	var open *Incident
	incidentIDs := make([]string, 0, len(s.incidents))
	for id := range s.incidents {
		incidentIDs = append(incidentIDs, id)
	}
	sort.Strings(incidentIDs)
	for _, id := range incidentIDs {
		incident := s.incidents[id]
		if incident.Auto && incident.ResolvedAtUnix == 0 && len(incident.Affects) == 1 && incident.Affects[0] == componentID {
			value := incident
			open = &value
			break
		}
	}
	if evaluation.State == "declared" {
		component := s.components[componentID]
		severity := "minor"
		if evaluation.Status == StatusOutage {
			severity = "moderate"
			if component.Critical {
				severity = "major"
			}
		} else if component.Critical {
			severity = "moderate"
		}
		if open == nil {
			id := "auto/" + componentID + "/" + fmt.Sprint(now)
			s.incidents[id] = Incident{ID: id, Title: component.Name + " — " + severity, Summary: "Automatically opened from monitoring quorum.", Status: "investigating", Severity: severity, Affects: []string{componentID}, Auto: true, StartedAtUnix: now, CreatedAtUnix: now}
			s.updates = append(s.updates, IncidentUpdate{ID: id + "/opened", IncidentID: id, AtUnix: now, Label: "investigating", Body: "Monitoring quorum declared an incident.", Author: "engine"})
		} else if open.Severity != severity {
			open.Severity = severity
			s.incidents[open.ID] = *open
			s.updates = append(s.updates, IncidentUpdate{ID: open.ID + "/severity/" + fmt.Sprint(now), IncidentID: open.ID, AtUnix: now, Label: "updated", Body: "Monitoring severity changed to " + severity + ".", Author: "engine"})
		}
		return
	}
	if evaluation.State == "operational" && evaluation.HasLiveReads && open != nil {
		open.Status = "resolved"
		open.ResolvedAtUnix = now
		s.incidents[open.ID] = *open
		s.updates = append(s.updates, IncidentUpdate{ID: open.ID + "/resolved/" + fmt.Sprint(now), IncidentID: open.ID, AtUnix: now, Label: "resolved", Body: "Monitoring reports recovery.", Author: "engine"})
	}
}

func (s *monitorState) evaluate(componentID string, now int64) ComponentEvaluation {
	component := s.components[componentID]
	latest := map[string]Observation{}
	for _, observation := range s.observations {
		if observation.ComponentID == componentID {
			current, ok := latest[observation.SourceID]
			if !ok || observation.ObservedAtUnix > current.ObservedAtUnix || (observation.ObservedAtUnix == current.ObservedAtUnix && observation.ID > current.ID) {
				latest[observation.SourceID] = observation
			}
		}
	}
	expected := map[string]bool{}
	for _, mapping := range s.mappings {
		if mapping.ComponentID == componentID {
			expected[mapping.SourceID] = true
		}
	}
	evaluation := ComponentEvaluation{ComponentID: componentID, Status: component.FallbackStatus, State: "operational"}
	monitorNonOK, monitorNonOKWeight, trustedNonOK, manualNonOK, manualOK, worst := 0, 0, 0, false, false, StatusOperational
	for sourceID, source := range s.sources {
		if source.Revoked {
			continue
		}
		observation, exists := latest[sourceID]
		if !exists {
			if expected[sourceID] && source.DefaultTTL != nil && *source.DefaultTTL > 0 {
				evaluation.StaleCount++
			}
			continue
		}
		stale := observation.ExpiresAtUnix != 0 && observation.ExpiresAtUnix <= now
		read := SourceRead{SourceID: source.ID, SourceName: source.Name, Weight: source.Weight, Trusted: source.Trusted, Kind: source.Kind, Signal: observation.Signal, ObservedAtUnix: observation.ObservedAtUnix, ExpiresAtUnix: observation.ExpiresAtUnix, Stale: stale}
		evaluation.Reads = append(evaluation.Reads, read)
		if stale {
			evaluation.StaleCount++
			continue
		}
		evaluation.HasLiveReads = true
		if observation.Signal == SignalOK {
			if source.Kind == SourceManual {
				manualOK = true
			}
			continue
		}
		evaluation.NonOKCount++
		evaluation.NonOKWeight += source.Weight
		candidate := StatusDegraded
		if observation.Signal == SignalDown {
			candidate = StatusOutage
		}
		if severityRank(candidate) > severityRank(worst) {
			worst = candidate
		}
		if source.Kind == SourceManual {
			manualNonOK = true
			continue
		}
		monitorNonOK++
		monitorNonOKWeight += source.Weight
		if source.Trusted {
			trustedNonOK++
			evaluation.TrustedNonOKCount++
		}
	}
	sort.Slice(evaluation.Reads, func(i, j int) bool { return evaluation.Reads[i].SourceID < evaluation.Reads[j].SourceID })
	evaluation.ReducedCoverage = evaluation.StaleCount > 0
	// Two independent monitors declare. A lone
	// trusted first-party source is visible at a degraded ceiling; a lone
	// untrusted monitor stays a watch. A manual non-OK is an explicit operator
	// declaration, but a manual OK cannot hide corroborated monitor evidence.
	if monitorNonOK >= 2 {
		evaluation.State, evaluation.Status = "declared", worst
	} else if manualNonOK {
		evaluation.State, evaluation.Status = "declared", worst
	} else if trustedNonOK > 0 {
		evaluation.State, evaluation.Status = "declared", worst
	} else if monitorNonOK == 1 {
		evaluation.State, evaluation.Status = "watch", StatusOperational
	}
	if manualOK && monitorNonOK < 2 {
		evaluation.State, evaluation.Status = "operational", StatusOperational
	}
	if evaluation.State == "declared" {
		evaluation.Level = string(evaluation.Status)
	}
	return evaluation
}

func (s *monitorState) projection(atUnix int64) Projection {
	return s.projectionWithOptions(atUnix, false, false)
}

func (s *monitorState) projectionWithOptions(atUnix int64, includeArchived, includeObservations bool) Projection {
	items := make([]ComponentProjection, 0, len(s.components))
	for _, component := range s.components {
		if component.Archived && !includeArchived {
			continue
		}
		own := s.evaluate(component.ID, atUnix)
		component.Uptime90D = append([]UptimeDay(nil), component.Uptime90D...)
		items = append(items, ComponentProjection{
			Component:     component,
			OwnEvaluation: own,
			Evaluation:    own,
		})
	}
	// Children bubble their worst status to every ancestor, while each leaf keeps
	// its own quorum decision. This makes arbitrary-depth public trees coherent.
	byID := map[string]int{}
	for i := range items {
		byID[items[i].Component.ID] = i
	}
	for _, item := range items {
		for parentID := item.Component.ParentID; parentID != ""; parentID = s.components[parentID].ParentID {
			if index, ok := byID[parentID]; ok && severityRank(item.Evaluation.Status) > severityRank(items[index].Evaluation.Status) {
				items[index].Evaluation.Status = item.Evaluation.Status
				items[index].Evaluation.State = "bubbled"
				items[index].Evaluation.Level = string(item.Evaluation.Status)
			}
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Component.SortOrder != items[j].Component.SortOrder {
			return items[i].Component.SortOrder < items[j].Component.SortOrder
		}
		return items[i].Component.ID < items[j].Component.ID
	})
	projection := Projection{Version: ContractVersion, Revision: s.revision, Components: items, Sources: sortedSources(s.sources), Mappings: sortedMappings(s.mappings), IncidentUpdates: append([]IncidentUpdate(nil), s.updates...), Maintenance: sortedMaintenance(s.maintenance)}
	if includeObservations {
		projection.Observations = append([]Observation(nil), s.observations...)
		sort.Slice(projection.Observations, func(i, j int) bool {
			if projection.Observations[i].ObservedAtUnix != projection.Observations[j].ObservedAtUnix {
				return projection.Observations[i].ObservedAtUnix < projection.Observations[j].ObservedAtUnix
			}
			return projection.Observations[i].ID < projection.Observations[j].ID
		})
	}
	for _, incident := range s.incidents {
		projection.Incidents = append(projection.Incidents, incident)
	}
	sort.Slice(projection.Incidents, func(i, j int) bool {
		if projection.Incidents[i].StartedAtUnix != projection.Incidents[j].StartedAtUnix {
			return projection.Incidents[i].StartedAtUnix > projection.Incidents[j].StartedAtUnix
		}
		return projection.Incidents[i].ID < projection.Incidents[j].ID
	})
	sort.Slice(projection.IncidentUpdates, func(i, j int) bool {
		if projection.IncidentUpdates[i].AtUnix != projection.IncidentUpdates[j].AtUnix {
			return projection.IncidentUpdates[i].AtUnix > projection.IncidentUpdates[j].AtUnix
		}
		return projection.IncidentUpdates[i].ID < projection.IncidentUpdates[j].ID
	})
	return projection
}

func (s *monitorState) query(query Query) (Projection, error) {
	projection := s.projectionWithOptions(query.AtUnix, query.IncludeArchived, query.IncludeObservations)
	if query.ComponentID != "" {
		if _, ok := s.components[query.ComponentID]; !ok {
			return Projection{}, fmt.Errorf("component %q not found", query.ComponentID)
		}
		filtered := projection.Components[:0]
		for _, item := range projection.Components {
			if item.Component.ID == query.ComponentID {
				filtered = append(filtered, item)
			}
		}
		projection.Components = filtered
	}
	if query.SourceID != "" {
		if _, ok := s.sources[query.SourceID]; !ok {
			return Projection{}, fmt.Errorf("source %q not found", query.SourceID)
		}
		projection.Sources = filterSources(projection.Sources, query.SourceID)
		projection.Mappings = filterMappings(projection.Mappings, query.SourceID)
		if query.IncludeObservations {
			projection.Observations = filterObservations(projection.Observations, query.SourceID)
		}
	}
	if query.IncidentID != "" {
		if _, ok := s.incidents[query.IncidentID]; !ok {
			return Projection{}, fmt.Errorf("incident %q not found", query.IncidentID)
		}
		projection.Incidents = filterIncidents(projection.Incidents, query.IncidentID)
		projection.IncidentUpdates = filterIncidentUpdates(projection.IncidentUpdates, query.IncidentID)
	}
	if query.MaintenanceID != "" {
		if _, ok := s.maintenance[query.MaintenanceID]; !ok {
			return Projection{}, fmt.Errorf("maintenance %q not found", query.MaintenanceID)
		}
		projection.Maintenance = filterMaintenance(projection.Maintenance, query.MaintenanceID)
	}
	return projection, nil
}

func (s *monitorState) projectionFor(componentID string, atUnix int64) Projection {
	projection := s.projection(atUnix)
	filtered := projection.Components[:0]
	for _, item := range projection.Components {
		if item.Component.ID == componentID {
			filtered = append(filtered, item)
		}
	}
	projection.Components = filtered
	return projection
}

func (s *monitorState) descendantIDs(root string, includeArchived bool) []string {
	children := map[string][]string{}
	for id, component := range s.components {
		if includeArchived || !component.Archived {
			children[component.ParentID] = append(children[component.ParentID], id)
		}
	}
	for parent := range children {
		sort.Strings(children[parent])
	}
	var result []string
	var visit func(string)
	visit = func(id string) {
		result = append(result, id)
		for _, child := range children[id] {
			visit(child)
		}
	}
	visit(root)
	return result
}

func (s *monitorState) hasOpenIncident(componentID string) bool {
	for _, incident := range s.incidents {
		if incident.ResolvedAtUnix != 0 || incident.Status == "resolved" {
			continue
		}
		for _, affected := range incident.Affects {
			if affected == componentID {
				return true
			}
		}
	}
	return false
}

func (s *monitorState) hasUpdate(id string) bool {
	for _, update := range s.updates {
		if update.ID == id {
			return true
		}
	}
	return false
}

func (s *monitorState) validateLeafReferences(kind string, ids []string) error {
	if len(ids) == 0 {
		return fmt.Errorf("%s affects is required", kind)
	}
	for _, id := range canonicalStrings(ids) {
		component, ok := s.components[id]
		if !ok || component.Archived {
			return fmt.Errorf("%s component %q not found", kind, id)
		}
		if !isLeafKind(component.Kind) {
			return fmt.Errorf("%s component %q is not a leaf", kind, id)
		}
	}
	return nil
}

func ingestEvaluation(observationID string, evaluation ComponentEvaluation) *IngestEvaluation {
	state := evaluation.State
	if state == "operational" {
		state = "ok"
	}
	level := evaluation.Level
	if evaluation.Status == StatusOutage && state == "declared" {
		level = "major"
	}
	return &IngestEvaluation{
		ObservationID:   observationID,
		ComponentID:     evaluation.ComponentID,
		State:           state,
		Level:           level,
		NonOK:           evaluation.NonOKCount,
		Sources:         len(evaluation.Reads),
		ReducedCoverage: evaluation.ReducedCoverage,
	}
}

type domainState struct {
	incidents   map[string]Incident
	updateCount map[string]int
	lastUpdate  map[string]IncidentUpdate
	maintenance map[string]Maintenance
}

func (s *monitorState) domainSnapshot() domainState {
	out := domainState{incidents: map[string]Incident{}, updateCount: map[string]int{}, lastUpdate: map[string]IncidentUpdate{}, maintenance: map[string]Maintenance{}}
	for id, incident := range s.incidents {
		out.incidents[id] = incident
	}
	for _, update := range s.updates {
		out.updateCount[update.IncidentID]++
		current, ok := out.lastUpdate[update.IncidentID]
		if !ok || update.AtUnix > current.AtUnix || (update.AtUnix == current.AtUnix && update.ID > current.ID) {
			out.lastUpdate[update.IncidentID] = update
		}
	}
	for id, maintenance := range s.maintenance {
		out.maintenance[id] = maintenance
	}
	return out
}

func diffDomain(before, after domainState, commandID string, atUnix int64) []DomainTransition {
	var transitions []DomainTransition
	for id, current := range after.incidents {
		previous, existed := before.incidents[id]
		kind := ""
		switch {
		case !existed:
			kind = "incident.opened"
		case current.Status == "resolved" && previous.Status != "resolved":
			kind = "incident.resolved"
		case !incidentEqual(previous, current) || before.updateCount[id] != after.updateCount[id]:
			kind = "incident.updated"
		}
		if kind != "" {
			incident := current
			incident.Affects = append([]string(nil), current.Affects...)
			transition := DomainTransition{
				ID:                   transitionID(commandID, kind, id),
				Kind:                 kind,
				EntityID:             id,
				ComponentID:          firstString(current.Affects),
				AffectedComponentIDs: append([]string(nil), current.Affects...),
				Status:               current.Status,
				Severity:             current.Severity,
				AtUnix:               atUnix,
				Incident:             &incident,
			}
			if existed {
				transition.PreviousStatus = previous.Status
				transition.PreviousSeverity = previous.Severity
			}
			if update, ok := after.lastUpdate[id]; ok && (!existed || before.lastUpdate[id].ID != update.ID) {
				copy := update
				transition.IncidentUpdate = &copy
			}
			transitions = append(transitions, transition)
		}
	}
	for id, previous := range before.incidents {
		if _, ok := after.incidents[id]; !ok {
			incident := previous
			incident.Affects = append([]string(nil), previous.Affects...)
			transitions = append(transitions, DomainTransition{
				ID:                   transitionID(commandID, "incident.deleted", id),
				Kind:                 "incident.deleted",
				EntityID:             id,
				ComponentID:          firstString(previous.Affects),
				AffectedComponentIDs: append([]string(nil), previous.Affects...),
				Status:               previous.Status,
				Severity:             previous.Severity,
				AtUnix:               atUnix,
				Incident:             &incident,
			})
		}
	}
	for id, current := range after.maintenance {
		previous, existed := before.maintenance[id]
		kind := ""
		switch {
		case !existed:
			kind = "maintenance.created"
		case current.Cancelled && !previous.Cancelled:
			kind = "maintenance.cancelled"
		case !maintenanceEqual(previous, current):
			kind = "maintenance.updated"
		}
		if kind != "" {
			maintenance := current
			maintenance.Affects = append([]string(nil), current.Affects...)
			transition := DomainTransition{
				ID:                   transitionID(commandID, kind, id),
				Kind:                 kind,
				EntityID:             id,
				ComponentID:          firstString(current.Affects),
				AffectedComponentIDs: append([]string(nil), current.Affects...),
				AtUnix:               atUnix,
				Maintenance:          &maintenance,
			}
			if existed {
				if previous.Cancelled {
					transition.PreviousStatus = "cancelled"
				} else {
					transition.PreviousStatus = "scheduled"
				}
			}
			if current.Cancelled {
				transition.Status = "cancelled"
			} else {
				transition.Status = "scheduled"
			}
			transitions = append(transitions, transition)
		}
	}
	for id, previous := range before.maintenance {
		if _, ok := after.maintenance[id]; !ok {
			maintenance := previous
			maintenance.Affects = append([]string(nil), previous.Affects...)
			transitions = append(transitions, DomainTransition{
				ID:                   transitionID(commandID, "maintenance.deleted", id),
				Kind:                 "maintenance.deleted",
				EntityID:             id,
				ComponentID:          firstString(previous.Affects),
				AffectedComponentIDs: append([]string(nil), previous.Affects...),
				Status:               "deleted",
				AtUnix:               atUnix,
				Maintenance:          &maintenance,
			})
		}
	}
	sort.Slice(transitions, func(i, j int) bool {
		if transitions[i].Kind != transitions[j].Kind {
			return transitions[i].Kind < transitions[j].Kind
		}
		return transitions[i].EntityID < transitions[j].EntityID
	})
	return transitions
}

func transitionID(commandID, kind, entityID string) string {
	return commandID + "/" + kind + "/" + entityID
}

func incidentEqual(a, b Incident) bool {
	return a.ID == b.ID && a.Title == b.Title && a.Summary == b.Summary && a.Status == b.Status && a.Severity == b.Severity && a.Auto == b.Auto && a.StartedAtUnix == b.StartedAtUnix && a.ResolvedAtUnix == b.ResolvedAtUnix && equalStrings(a.Affects, b.Affects)
}

func maintenanceEqual(a, b Maintenance) bool {
	return a.ID == b.ID && a.Title == b.Title && a.Summary == b.Summary && a.Kind == b.Kind && a.ScheduledStartUnix == b.ScheduledStartUnix && a.ScheduledEndUnix == b.ScheduledEndUnix && a.Cancelled == b.Cancelled && a.CancelledAtUnix == b.CancelledAtUnix && equalStrings(a.Affects, b.Affects)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func isLeafKind(kind string) bool { return kind == "service" || kind == "host" }
func validSourceKind(kind SourceKind) bool {
	value := string(kind)
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' {
			continue
		}
		return false
	}
	return true
}
func validIncidentStatus(value string) bool {
	return value == "investigating" || value == "identified" || value == "monitoring" || value == "resolved"
}
func validIncidentSeverity(value string) bool {
	return value == "minor" || value == "moderate" || value == "major"
}
func validStatus(value Status) bool {
	return value == StatusOperational || value == StatusDegraded || value == StatusOutage
}
func severityRank(value Status) int {
	if value == StatusOutage {
		return 2
	}
	if value == StatusDegraded {
		return 1
	}
	return 0
}
func canonicalStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	deduped := out[:0]
	for _, value := range out {
		if value != "" && (len(deduped) == 0 || deduped[len(deduped)-1] != value) {
			deduped = append(deduped, value)
		}
	}
	return deduped
}
func sortedSources(values map[string]Source) []Source {
	out := make([]Source, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAtUnix != out[j].CreatedAtUnix {
			return out[i].CreatedAtUnix < out[j].CreatedAtUnix
		}
		return out[i].ID < out[j].ID
	})
	return out
}
func sortedMappings(values map[string]SourceTargetMapping) []SourceTargetMapping {
	out := make([]SourceTargetMapping, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func sortedMaintenance(values map[string]Maintenance) []Maintenance {
	out := make([]Maintenance, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ScheduledStartUnix != out[j].ScheduledStartUnix {
			return out[i].ScheduledStartUnix < out[j].ScheduledStartUnix
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func filterSources(values []Source, id string) []Source {
	out := values[:0]
	for _, value := range values {
		if value.ID == id {
			out = append(out, value)
		}
	}
	return out
}

func filterMappings(values []SourceTargetMapping, sourceID string) []SourceTargetMapping {
	out := values[:0]
	for _, value := range values {
		if value.SourceID == sourceID {
			out = append(out, value)
		}
	}
	return out
}

func filterObservations(values []Observation, sourceID string) []Observation {
	out := values[:0]
	for _, value := range values {
		if value.SourceID == sourceID {
			out = append(out, value)
		}
	}
	return out
}

func filterIncidents(values []Incident, id string) []Incident {
	out := values[:0]
	for _, value := range values {
		if value.ID == id {
			out = append(out, value)
		}
	}
	return out
}

func filterIncidentUpdates(values []IncidentUpdate, incidentID string) []IncidentUpdate {
	out := values[:0]
	for _, value := range values {
		if value.IncidentID == incidentID {
			out = append(out, value)
		}
	}
	return out
}

func filterMaintenance(values []Maintenance, id string) []Maintenance {
	out := values[:0]
	for _, value := range values {
		if value.ID == id {
			out = append(out, value)
		}
	}
	return out
}
