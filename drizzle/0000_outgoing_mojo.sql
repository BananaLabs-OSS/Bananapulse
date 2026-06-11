CREATE TABLE "admin_magic_links" (
	"token" text PRIMARY KEY NOT NULL,
	"email" text NOT NULL,
	"expires_at" timestamp with time zone NOT NULL,
	"used_at" timestamp with time zone,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL
);
--> statement-breakpoint
CREATE TABLE "admin_sessions" (
	"token" text PRIMARY KEY NOT NULL,
	"email" text NOT NULL,
	"expires_at" timestamp with time zone NOT NULL,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL
);
--> statement-breakpoint
CREATE TABLE "api_tokens" (
	"id" text PRIMARY KEY NOT NULL,
	"name" text NOT NULL,
	"token_hash" text NOT NULL,
	"scope" text DEFAULT 'full' NOT NULL,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL,
	"last_used_at" timestamp with time zone,
	"expires_at" timestamp with time zone,
	"revoked_at" timestamp with time zone
);
--> statement-breakpoint
CREATE TABLE "components" (
	"id" text PRIMARY KEY NOT NULL,
	"parent_id" text,
	"name" text NOT NULL,
	"kind" text DEFAULT 'service' NOT NULL,
	"tag" text,
	"status" text DEFAULT 'ok' NOT NULL,
	"uptime_90d" jsonb DEFAULT '[]' NOT NULL,
	"sort_order" integer DEFAULT 0 NOT NULL,
	"brand" text,
	"domain" text,
	"launched" boolean DEFAULT true NOT NULL,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL,
	"archived_at" timestamp with time zone
);
--> statement-breakpoint
CREATE TABLE "incident_timeline" (
	"id" text PRIMARY KEY NOT NULL,
	"incident_id" text,
	"at" timestamp with time zone NOT NULL,
	"label" text NOT NULL,
	"body" text NOT NULL,
	"author" text DEFAULT 'engine' NOT NULL,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL
);
--> statement-breakpoint
CREATE TABLE "incidents" (
	"id" text PRIMARY KEY NOT NULL,
	"title" text NOT NULL,
	"summary" text NOT NULL,
	"status" text NOT NULL,
	"severity" text NOT NULL,
	"affects" text[] NOT NULL,
	"auto" boolean DEFAULT false NOT NULL,
	"started_at" timestamp with time zone NOT NULL,
	"resolved_at" timestamp with time zone,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL
);
--> statement-breakpoint
CREATE TABLE "maintenance" (
	"id" text PRIMARY KEY NOT NULL,
	"title" text NOT NULL,
	"summary" text NOT NULL,
	"scheduled_start" timestamp with time zone NOT NULL,
	"scheduled_end" timestamp with time zone NOT NULL,
	"affects" text[] NOT NULL,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL
);
--> statement-breakpoint
CREATE TABLE "observations" (
	"id" text PRIMARY KEY NOT NULL,
	"source_id" text NOT NULL,
	"component_id" text NOT NULL,
	"signal" text NOT NULL,
	"detail" text,
	"observed_at" timestamp with time zone DEFAULT now() NOT NULL,
	"expires_at" timestamp with time zone
);
--> statement-breakpoint
CREATE TABLE "source_target_map" (
	"id" text PRIMARY KEY NOT NULL,
	"source_id" text NOT NULL,
	"raw_label" text NOT NULL,
	"component_id" text NOT NULL
);
--> statement-breakpoint
CREATE TABLE "sources" (
	"id" text PRIMARY KEY NOT NULL,
	"name" text NOT NULL,
	"token_hash" text NOT NULL,
	"weight" integer DEFAULT 1 NOT NULL,
	"kind" text DEFAULT 'push' NOT NULL,
	"trusted" boolean DEFAULT false NOT NULL,
	"default_ttl" integer,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL,
	"revoked_at" timestamp with time zone
);
--> statement-breakpoint
CREATE TABLE "subscribers" (
	"id" text PRIMARY KEY NOT NULL,
	"email" text NOT NULL,
	"confirmed_at" timestamp with time zone,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL,
	CONSTRAINT "subscribers_email_unique" UNIQUE("email")
);
--> statement-breakpoint
ALTER TABLE "components" ADD CONSTRAINT "components_parent_id_components_id_fk" FOREIGN KEY ("parent_id") REFERENCES "public"."components"("id") ON DELETE no action ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "incident_timeline" ADD CONSTRAINT "incident_timeline_incident_id_incidents_id_fk" FOREIGN KEY ("incident_id") REFERENCES "public"."incidents"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "observations" ADD CONSTRAINT "observations_source_id_sources_id_fk" FOREIGN KEY ("source_id") REFERENCES "public"."sources"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "observations" ADD CONSTRAINT "observations_component_id_components_id_fk" FOREIGN KEY ("component_id") REFERENCES "public"."components"("id") ON DELETE no action ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "source_target_map" ADD CONSTRAINT "source_target_map_source_id_sources_id_fk" FOREIGN KEY ("source_id") REFERENCES "public"."sources"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "source_target_map" ADD CONSTRAINT "source_target_map_component_id_components_id_fk" FOREIGN KEY ("component_id") REFERENCES "public"."components"("id") ON DELETE no action ON UPDATE no action;--> statement-breakpoint
CREATE INDEX "api_tokens_hash_active_idx" ON "api_tokens" USING btree ("token_hash");--> statement-breakpoint
CREATE INDEX "components_parent_idx" ON "components" USING btree ("parent_id");--> statement-breakpoint
CREATE INDEX "components_kind_idx" ON "components" USING btree ("kind");--> statement-breakpoint
CREATE INDEX "incident_timeline_incident_at_idx" ON "incident_timeline" USING btree ("incident_id","at");--> statement-breakpoint
CREATE INDEX "incidents_status_idx" ON "incidents" USING btree ("status");--> statement-breakpoint
CREATE INDEX "incidents_started_at_idx" ON "incidents" USING btree ("started_at");--> statement-breakpoint
CREATE INDEX "observations_component_observed_idx" ON "observations" USING btree ("component_id","observed_at");--> statement-breakpoint
CREATE INDEX "observations_source_component_observed_idx" ON "observations" USING btree ("source_id","component_id","observed_at");--> statement-breakpoint
CREATE INDEX "source_target_map_lookup_idx" ON "source_target_map" USING btree ("source_id","raw_label");--> statement-breakpoint
CREATE INDEX "sources_token_hash_idx" ON "sources" USING btree ("token_hash");