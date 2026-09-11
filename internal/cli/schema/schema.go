package schema

import (
	"maps"
	"strings"

	"github.com/matcra587/jira-cli/internal/cli/cache/registry"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/errtax"
	"github.com/spf13/cobra"
)

func schemaKeyForCommand(cmd *cobra.Command) string {
	path := strings.TrimSpace(strings.TrimPrefix(cmd.CommandPath(), "jira"))
	if path == "" {
		return "root"
	}
	return strings.ReplaceAll(path, " ", ".")
}

func outputSchemas() map[string]any {
	errorSchema := map[string]any{
		"type":     "object",
		"required": []string{"type", "code", "message", "hint", "retryable"},
		"properties": map[string]any{
			"type": map[string]any{
				"type": "string",
				"enum": []errtax.Type{
					errtax.TypeAuth,
					errtax.TypeNotFound,
					errtax.TypeValidation,
					errtax.TypeRateLimit,
					errtax.TypeServer,
					errtax.TypeIO,
				},
			},
			"code":                map[string]any{"type": "string", "enum": errtax.Codes()},
			"message":             map[string]any{"type": "string"},
			"hint":                map[string]any{"type": "string"},
			"retryable":           map[string]any{"type": "boolean"},
			"flag":                map[string]any{"type": "string"},
			"field":               map[string]any{"type": "string"},
			"path":                map[string]any{"type": "string"},
			"http_status":         map[string]any{"type": "integer"},
			"retry_after_seconds": map[string]any{"type": "integer"},
			"provider":            map[string]any{"type": "string"},
			"upstream_code":       map[string]any{"type": "string"},
			"upstream_status":     map[string]any{"type": "integer"},
			"upstream_request_id": map[string]any{"type": "string", "description": "Jira's own trace id (Atl-Traceid / X-ARequestId) for the failed exchange — quote it to Atlassian support. meta.request_id is local and has no server-side meaning."},
		},
	}
	envelopeSchema := map[string]any{
		"type":     "object",
		"required": []string{"ok", "meta", "data", "errors", "warnings"},
		"properties": map[string]any{
			"ok": map[string]any{"type": "boolean"},
			"meta": map[string]any{
				"type":     "object",
				"required": []string{"command", "timestamp"},
				"properties": map[string]any{
					"command":             map[string]any{"type": "string"},
					"exit_code":           map[string]any{"type": "integer", "description": "Present only on failure envelopes."},
					"timestamp":           map[string]any{"type": "string", "format": "date-time"},
					"request_id":          map[string]any{"type": "string", "description": "Locally generated correlation id; no server-side meaning."},
					"upstream_request_id": map[string]any{"type": "string", "description": "Jira's own trace id (Atl-Traceid / X-ARequestId) for the exchange; present when the command had a Jira response to read it from."},
					"pagination": map[string]any{
						"type":        "object",
						"description": "Canonical pagination block, identical on every paginated command: meta.pagination on single-target reads, and the same shape at results[].data.pagination inside keyed multi-key results. isLast and nextCursor are the authoritative walk signals. Mutation envelopes omit the block entirely.",
						"required":    []string{"startAt", "maxResults", "isLast"}, // pagination-exempt: documents output-shape only
						"properties": map[string]any{
							"startAt":    map[string]any{"type": "integer"}, // pagination-exempt: output-shape only
							"maxResults": map[string]any{"type": "integer"},
							"total":      map[string]any{"type": "integer", "description": "Present only when the endpoint reports an authoritative total; token-paged endpoints (enhanced search) never do."},
							"isLast":     map[string]any{"type": "boolean"},
							"nextCursor": map[string]any{"type": "string", "description": "Pass back via --cursor (search jql, issue list) to fetch the next page."},
						},
					},
				},
			},
			"data": map[string]any{"type": []string{"object", "array", "null"}},
			"errors": map[string]any{
				"type":  "array",
				"items": errorSchema,
			},
			"warnings": map[string]any{"type": "array"},
		},
	}
	// Shared building blocks for the per-command entries below.
	commentUser := map[string]any{
		"type":        []string{"object", "null"},
		"description": "Jira user identity; null when Jira reports none (e.g. an anonymized author).",
		"properties": map[string]any{
			"account_id":    map[string]any{"type": "string"},
			"display_name":  map[string]any{"type": "string"},
			"email_address": map[string]any{"type": "string"},
		},
	}
	worklogEntry := map[string]any{
		"type":        "object",
		"description": "Jira worklog in its native shape.",
		"properties": map[string]any{
			"id":               map[string]any{"type": "string"},
			"timeSpentSeconds": map[string]any{"type": "integer"},
			"started":          map[string]any{"type": "string"},
			"comment":          map[string]any{"type": []string{"object", "null"}, "description": "ADF document; null or absent when the worklog carries no comment."},
		},
	}
	// The contract-v2 issue identity: data.issue is always an object with at
	// least key (see cmdutil.IssueRef), never a bare string.
	issueRef := map[string]any{
		"type":     "object",
		"required": []string{"key"},
		"properties": map[string]any{
			"id":   map[string]any{"type": "string"},
			"key":  map[string]any{"type": "string"},
			"self": map[string]any{"type": "string"},
		},
	}
	// The optional post-write verification block create/edit emit under
	// --verify (see internal/cli/issue/verify.go verificationResult).
	verificationBlock := map[string]any{
		"type":        "object",
		"description": "Present on --verify live writes: the re-fetch diff of requested fields.",
		"required":    []string{"applied", "dropped"},
		"properties": map[string]any{
			"applied":    map[string]any{"type": "object"},
			"dropped":    map[string]any{"type": "array"},
			"unverified": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Requested fields the typed diff cannot observe (ADF description, duedate, ...)."},
		},
	}
	// Attachment authors carry no email_address: the attachment wire shape
	// projects account_id and display_name only.
	attachmentUser := map[string]any{
		"type":        []string{"object", "null"},
		"description": "Jira user identity; null when Jira reports none.",
		"properties": map[string]any{
			"account_id":   map[string]any{"type": "string"},
			"display_name": map[string]any{"type": "string"},
		},
	}
	linkType := map[string]any{
		"type":     "object",
		"required": []string{"id", "name", "inward", "outward"},
		"properties": map[string]any{
			"id":      map[string]any{"type": "string"},
			"name":    map[string]any{"type": "string"},
			"inward":  map[string]any{"type": "string"},
			"outward": map[string]any{"type": "string"},
		},
	}
	// The cache_state trio plus the from_cache/fetched_at pair every
	// cache-backed read reports (AddCacheStateFields + the primer data).
	cacheStateProperties := map[string]any{
		"from_cache":         map[string]any{"type": "boolean"},
		"fetched_at":         map[string]any{"type": "string", "format": "date-time"},
		"cache_state":        map[string]any{"type": "string", "description": "Effective disposition after the read: fresh, stale, missing, malformed, refresh, or empty."},
		"cache_source_state": map[string]any{"type": "string", "description": "Disposition observed before any fetch (never the derived empty state)."},
		"cache_empty":        map[string]any{"type": "boolean"},
	}
	keyedResults := map[string]any{
		"type":        "object",
		"description": "Canonical keyed multi-target result set: ordered per-key rows with independent ok/error outcomes; one failed key does not roll back the others.",
		"required":    []string{"results", "succeeded", "failed"},
		"properties": map[string]any{
			"results": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":     "object",
					"required": []string{"key", "ok"},
					"properties": map[string]any{
						"key":   map[string]any{"type": "string"},
						"ok":    map[string]any{"type": "boolean"},
						"data":  map[string]any{"type": "object", "description": "Present when ok is true; the command's single-target data shape."},
						"error": errorSchema,
					},
				},
			},
			"succeeded": map[string]any{"type": "integer"},
			"failed":    map[string]any{"type": "integer"},
		},
	}
	out := map[string]any{
		"envelope":      envelopeSchema,
		"error":         errorSchema,
		"keyed_results": keyedResults,
		"issue.view": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"issue": map[string]any{
					"type":        "object",
					"description": "Present for single-key issue view success. Carries Jira's issue shape plus `transitions` (valid workflow moves from the current status) and `editmeta.fields` (editable fields with required/operations/allowedValues).",
				},
				"results": map[string]any{
					"type":        "array",
					"description": "Present for multi-key issue view; ordered like the requested keys.",
					"items": map[string]any{
						"type":     "object",
						"required": []string{"key", "ok"},
						"properties": map[string]any{
							"key":   map[string]any{"type": "string"},
							"ok":    map[string]any{"type": "boolean"},
							"issue": map[string]any{"type": "object", "description": "Present when ok is true."},
							"error": errorSchema,
						},
					},
				},
				"succeeded": map[string]any{"type": "integer"},
				"failed":    map[string]any{"type": "integer"},
			},
		},
		"issue.list": map[string]any{
			"type":     "object",
			"required": []string{"issues"},
			"properties": map[string]any{
				"issues": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type":     "object",
						"required": []string{"key", "summary", "status", "status_category", "updated"},
						"properties": map[string]any{
							"key":     map[string]any{"type": "string"},
							"summary": map[string]any{"type": "string"},
							"status":  map[string]any{"type": "string"},
							"status_category": map[string]any{
								"type":        "string",
								"description": "Workflow category key (new, indeterminate, done); empty when the status carries none.",
							},
							"status_color": map[string]any{
								"type":        "string",
								"description": "Status category color name; empty when the status carries none.",
							},
							"assignee": map[string]any{
								"type": []string{"object", "null"},
								"properties": map[string]any{
									"account_id":   map[string]any{"type": "string"},
									"display_name": map[string]any{"type": "string"},
								},
							},
							"priority": map[string]any{"type": []string{"string", "null"}},
							"updated":  map[string]any{"type": "string", "format": "date-time"},
						},
					},
				},
				"detail": map[string]any{"type": "boolean"},
				"board_scope": map[string]any{
					"type":        "object",
					"description": "Always present. Reports the resolved board and whether its scope was applied; applied is false on unscoped lists.",
					"properties": map[string]any{
						"applied":      map[string]any{"type": "boolean"},
						"project_keys": map[string]any{"type": "array"},
						"id":           map[string]any{"type": "integer"},
						"name":         map[string]any{"type": "string"},
						"type":         map[string]any{"type": "string"},
					},
				},
				"jql": map[string]any{
					"type":        "string",
					"description": "Always present; the JQL the query resolved to.",
				},
				"precedence": map[string]any{
					"type":        "string",
					"description": "Always present; which scope source won when several were set (\"none\" when unscoped).",
				},
				"count": map[string]any{"type": "integer", "description": "Present only under --count (meta.command issue.list.count); issues is absent on that variant."},
				"url":   map[string]any{"type": "string", "description": "Present only under --as-jql (meta.command issue.list.jql); the Jira search URL for the resolved JQL."},
				"succeeded_key_chunks": map[string]any{
					"type":        "integer",
					"description": "Present when chunked --key reads partially fail.",
				},
				"failed_key_chunks": map[string]any{
					"type":        "array",
					"description": "Present when chunked --key reads partially fail.",
					"items": map[string]any{
						"type":     "object",
						"required": []string{"key_expr", "error"},
						"properties": map[string]any{
							"key_expr": map[string]any{"type": "string"},
							"error":    errorSchema,
						},
					},
				},
			},
		},
		"issue.create": map[string]any{
			"type":     "object",
			"required": []string{"dry_run"},
			"properties": map[string]any{
				"issue":              map[string]any{"type": "object", "description": "The created issue on a successful live write."},
				"preview":            map[string]any{"type": "object", "description": "The validated payload submitted by a live write or proposed by a dry-run."},
				"dry_run":            map[string]any{"type": "boolean"},
				"validated_remotely": map[string]any{"type": "boolean", "description": "Whether resolved Jira create-screen metadata validated at least one field."},
				"verification":       verificationBlock,
			},
		},
		"issue.edit": map[string]any{
			"type":     "object",
			"required": []string{"issue", "dry_run", "fields"},
			"properties": map[string]any{
				"issue":              issueRef,
				"dry_run":            map[string]any{"type": "boolean"},
				"fields":             map[string]any{"type": "object", "description": "The validated field payload proposed by a dry-run or submitted by a live write."},
				"result":             map[string]any{"type": "object", "description": "The updated issue returned after a successful live write."},
				"update":             map[string]any{"type": "object", "description": "Native Jira update operations proposed by a dry-run or submitted by a live write."},
				"validated_remotely": map[string]any{"type": "boolean", "description": "Whether resolved Jira edit-screen metadata validated at least one field."},
				"verification":       verificationBlock,
			},
		},
		"worklog.add": map[string]any{
			"type":     "object",
			"required": []string{"issue", "worklog", "dry_run"},
			"properties": map[string]any{
				"issue":   issueRef,
				"worklog": worklogEntry,
				"dry_run": map[string]any{"type": "boolean"},
			},
		},
		"worklog.list": map[string]any{
			"type":     "object",
			"required": []string{"issue", "worklogs"},
			"properties": map[string]any{
				"issue":    issueRef,
				"worklogs": map[string]any{"type": "array", "items": worklogEntry},
			},
		},
		"issue.comment.list": map[string]any{
			"type":        "object",
			"description": "Single-key form; multi-key reads carry the same object at results[].data with its pagination block inside.",
			"required":    []string{"comments"},
			"properties": map[string]any{
				"pagination": map[string]any{"type": "object", "description": "Present only inside multi-key results[].data; single-key reads report pagination at meta.pagination."},
				"comments": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type":     "object",
						"required": []string{"body", "author", "update_author", "visibility"},
						"properties": map[string]any{
							"id":            map[string]any{"type": "string"},
							"body":          map[string]any{"type": []string{"object", "null"}, "description": "Native ADF document; null when the comment has no body."},
							"author":        commentUser,
							"update_author": commentUser,
							"created":       map[string]any{"type": "string", "format": "date-time"},
							"updated":       map[string]any{"type": "string", "format": "date-time"},
							"visibility": map[string]any{
								"type":        []string{"object", "null"},
								"description": "Role/group restriction; null when the comment is unrestricted.",
								"properties": map[string]any{
									"type":  map[string]any{"type": "string"},
									"value": map[string]any{"type": "string"},
								},
							},
						},
					},
				},
			},
		},
		"issue.attachment.list": map[string]any{
			"type":        "object",
			"description": "Single-key form; multi-key reads carry the same object at results[].data with its pagination block inside.",
			"required":    []string{"attachments"},
			"properties": map[string]any{
				"pagination": map[string]any{"type": "object", "description": "Present only inside multi-key results[].data; single-key reads report pagination at meta.pagination."},
				"attachments": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type":     "object",
						"required": []string{"id", "filename", "mime_type", "size", "created", "author"},
						"properties": map[string]any{
							"id":        map[string]any{"type": "string"},
							"filename":  map[string]any{"type": "string"},
							"mime_type": map[string]any{"type": "string"},
							"size":      map[string]any{"type": "integer"},
							"created":   map[string]any{"type": "string", "format": "date-time"},
							"author":    attachmentUser,
						},
					},
				},
			},
		},
		"issue.link.list": map[string]any{
			"type":     "object",
			"required": []string{"issue", "links", "count"},
			"properties": map[string]any{
				"issue": issueRef,
				"count": map[string]any{"type": "integer"},
				"links": map[string]any{
					"type":        "array",
					"description": "Jira's inward/outward fork flattened into one direction-aware array, sorted by (direction, type.name, other_issue.key).",
					"items": map[string]any{
						"type":     "object",
						"required": []string{"id", "type", "direction", "other_issue"},
						"properties": map[string]any{
							"id":        map[string]any{"type": "string"},
							"self":      map[string]any{"type": "string"},
							"type":      linkType,
							"direction": map[string]any{"type": "string", "enum": []string{"inward", "outward"}},
							"other_issue": map[string]any{
								"type":     "object",
								"required": []string{"key"},
								"properties": map[string]any{
									"key":     map[string]any{"type": "string"},
									"summary": map[string]any{"type": "string"},
									"status":  map[string]any{"type": "string"},
								},
							},
						},
					},
				},
			},
		},
		"issue.link.types": map[string]any{
			"type":     "object",
			"required": []string{"link_types", "count"},
			"properties": mergeProperties(map[string]any{
				"link_types": map[string]any{"type": "array", "items": linkType},
				"count":      map[string]any{"type": "integer"},
			}, cacheStateProperties),
		},
		"issue.transition": map[string]any{
			"type":        "object",
			"description": "Dual-form: with a target STATUS the data reports the applied transition; without one the command is a read that lists the available transitions.",
			"required":    []string{"issue"},
			"properties": map[string]any{
				"issue":                issueRef,
				"transition":           map[string]any{"type": "string", "description": "Resolved transition id; present when a target was applied (or validated under --dry-run --validate-remote)."},
				"transition_validated": map[string]any{"type": "boolean", "description": "Present on --dry-run --validate-remote."},
				"dry_run":              map[string]any{"type": "boolean"},
				"fields":               map[string]any{"type": "object", "description": "Present on --dry-run when the payload carried transition-screen fields."},
				"comment":              map[string]any{"type": "object", "description": "Present on --dry-run when the payload carried an ADF comment."},
				"update":               map[string]any{"type": "object", "description": "Present on --dry-run when the payload carried an update operation block."},
				"transitions": map[string]any{
					"type":        "array",
					"description": "Present on the no-target list form.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id":        map[string]any{"type": "string"},
							"name":      map[string]any{"type": "string"},
							"hasScreen": map[string]any{"type": "boolean"},
						},
					},
				},
			},
		},
		"boards.list": map[string]any{
			"type":     "object",
			"required": []string{"boards"},
			"properties": mergeProperties(map[string]any{
				"boards": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type":     "object",
						"required": []string{"id", "name", "type", "project_keys"},
						"properties": map[string]any{
							"id":           map[string]any{"type": "integer"},
							"name":         map[string]any{"type": "string"},
							"type":         map[string]any{"type": "string"},
							"project_keys": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						},
					},
				},
				"truncated":        map[string]any{"type": "boolean"},
				"truncated_reason": map[string]any{"type": "string"},
			}, cacheStateProperties),
		},
		"cache.boards": map[string]any{
			"type":     "object",
			"required": []string{"profile", "boards_count"},
			"properties": mergeProperties(map[string]any{
				"profile":          map[string]any{"type": "string"},
				"primed":           map[string]any{"type": "boolean", "description": "true when this invocation fetched from Jira and wrote the cache file."},
				"boards_count":     map[string]any{"type": "integer"},
				"ttl_seconds":      map[string]any{"type": "integer"},
				"truncated":        map[string]any{"type": "boolean"},
				"truncated_reason": map[string]any{"type": "string"},
			}, cacheStateProperties),
		},
		// issue.rank derives via withDerivedSchemas like every registered op;
		// only its prose lives here for the harvest.
		"issue.rank": map[string]any{
			"properties": map[string]any{
				"anchor":   map[string]any{"description": "The issue the ranked set was placed relative to."},
				"position": map[string]any{"enum": []string{"before", "after"}},
				"order":    map[string]any{"description": "The submitted issue order, preserved end-to-end across chunks."},
				"chunks":   map[string]any{"description": "How many 50-issue requests the set was split into."},
				"ranked":   map[string]any{"description": "false only on the no-profile degraded path."},
			},
		},
		"cache.issuekeys": map[string]any{
			"type":     "object",
			"required": []string{"profile", "issue_keys", "count"},
			"properties": map[string]any{
				"profile":    map[string]any{"type": "string"},
				"issue_keys": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Recently used issue keys, newest first — local state written as a side effect of commands touching keys, never fetched from Jira."},
				"count":      map[string]any{"type": "integer"},
			},
		},
		"cache.refresh": keyedResults,
		"cache.clear": map[string]any{
			"type":     "object",
			"required": []string{"profile", "removed"},
			"properties": map[string]any{
				"profile":  map[string]any{"type": "string"},
				"resource": map[string]any{"type": "string", "description": "Present when one resource was targeted; absent on a whole-profile clear."},
				"removed":  map[string]any{"type": []string{"integer", "boolean"}, "description": "File count on a whole-profile clear; whether the file existed on a single-resource clear. Under --dry-run, what a live clear would remove."},
				"dry_run":  map[string]any{"type": "boolean"},
			},
		},
	}
	// Every flat-list cache primer shares one envelope shape; only the key
	// the items ride under varies. Deriving the entries from the resource
	// registry keeps the schema in lockstep with the actual `cache <name>`
	// subcommands (boards is the special-cased non-list primer above).
	for _, r := range registry.Registry {
		if r.Fetch == nil {
			continue
		}
		out["cache."+r.Name] = map[string]any{
			"type":     "object",
			"required": []string{"profile", r.Key(), "count"},
			"properties": mergeProperties(map[string]any{
				"profile": map[string]any{"type": "string"},
				r.Key():   map[string]any{"type": "array", "description": "The cached " + r.Name + " list, served from disk or a fresh fetch."},
				"count":   map[string]any{"type": "integer"},
			}, cacheStateProperties),
		}
	}
	return withDerivedSchemas(out)
}

// mergeProperties combines a schema's own properties with a shared
// property block (the cache-state trio) without either side mutating the
// other.
func mergeProperties(own, shared map[string]any) map[string]any {
	merged := make(map[string]any, len(own)+len(shared))
	maps.Copy(merged, shared)
	maps.Copy(merged, own)
	return merged
}

// inputSchemas publishes the canonical input shapes the mutation
// commands accept via --json-input. The `adf_document` shape is the one
// canonical ADF document form: every command that takes a rich-text
// body — `issue create` (description), `issue edit` (fields.description),
// `issue comment` (body), and `worklog add` (comment) — accepts exactly
// this shape, and the dry-run preview and live submit both validate it.
//
// $ref pointers resolve against the envelope root: `adf_document` is
// emitted at data.input_schemas.adf_document, so the JSON pointer is
// "#/data/input_schemas/adf_document". A standard JSON-pointer resolver
// can dereference it directly against the command's envelope output.
func inputSchemas() map[string]any {
	adfDocument := map[string]any{
		"type":        "object",
		"description": "Canonical Atlassian Document Format (ADF) document. Accepted by every rich-text body field across the CLI.",
		"required":    []string{"type", "version", "content"},
		"properties": map[string]any{
			"type":    map[string]any{"type": "string", "enum": []string{"doc"}},
			"version": map[string]any{"type": "integer", "enum": []int{1}},
			"content": map[string]any{"type": "array", "description": "Block-level ADF nodes. May be empty."},
		},
		"additionalProperties": false,
	}
	// The comment body schema is registered under the runnable group (the
	// legacy `issue comment KEY` alias for add) AND its add/edit leaves:
	// agents discover the leaves, so a schema keyed only to the group would
	// leave the commands that actually take --json-input reporting no
	// input schema.
	commentBody := map[string]any{
		"type":        "object",
		"description": "issue comment --json-input body. The top-level object is a canonical ADF document (or {\"body\": <adf_document>}).",
		"$ref":        "#/data/input_schemas/adf_document",
	}
	return map[string]any{
		"adf_document": adfDocument,
		"issue.create": map[string]any{
			"type":        "object",
			"description": "issue create --json-input payload. Accepts the flat convenience keys shown here or the Jira-native {\"fields\": {...}} object interchangeably (wire spellings like project/issuetype work in either). Object-valued system fields also accept a bare string, lifted to one fixed identity key: project/parent -> {\"key\": ...}, issuetype/priority -> {\"name\": ...}, assignee/reporter -> {\"accountId\": ...}, and string elements of components/fixVersions/versions -> {\"name\": ...}. Explicit wire objects pass through untouched; there is no digits-means-id guessing. The description may be supplied as raw ADF or as Markdown.",
			"properties": map[string]any{
				"summary":              map[string]any{"type": "string"},
				"project_key":          map[string]any{"type": "string"},
				"issue_type":           map[string]any{"type": "string"},
				"fields":               map[string]any{"type": "object", "description": "Jira-native field set; treated as the field set when present."},
				"description":          map[string]any{"$ref": "#/data/input_schemas/adf_document"},
				"description_markdown": map[string]any{"type": "string", "description": "Markdown converted to ADF; mutually exclusive with description."},
			},
		},
		"issue.edit": map[string]any{
			"type":        "object",
			"description": "issue edit --json-input payload. {\"fields\": {...}} is canonical; bare field keys at the top level are accepted as the field set. Object-valued system fields accept a bare string exactly as on create, lifted to one fixed identity key: project/parent -> {\"key\": ...}, issuetype/priority -> {\"name\": ...}, assignee/reporter -> {\"accountId\": ...}, and string elements of components/fixVersions/versions -> {\"name\": ...}; explicit wire objects pass through untouched. ADF-shaped values inside fields (e.g. fields.description) are validated as canonical ADF documents. A top-level update block — the native PUT /rest/api/3/issue body's sibling section of add/set/remove operations — is forwarded verbatim; Jira validates the operation verbs.",
			"properties": map[string]any{
				"fields": map[string]any{"type": "object"},
				"update": map[string]any{"type": "object", "description": "Native REST add/set/remove operation block, sent as a sibling of fields."},
			},
		},
		"issue.transition": map[string]any{
			"type":        "object",
			"description": "issue transition --json-input payload. Accepts the exact POST /rest/api/3/issue/{key}/transitions body: a transition section naming the target ({\"id\": ...} or {\"name\": ...}), a fields object, and an update operation block — plus the convenience comment key (an ADF document posted atomically with the status change). The target may come from the payload or the command line; setting both to different values is an error.",
			"properties": map[string]any{
				"transition": map[string]any{"type": "object", "description": "Target transition by id or name; equivalent to the positional STATUS."},
				"fields":     map[string]any{"type": "object"},
				"update":     map[string]any{"type": "object", "description": "Native REST operation block, forwarded verbatim; mutually exclusive with the comment key when it carries comment operations."},
				"comment":    map[string]any{"$ref": "#/data/input_schemas/adf_document"},
			},
		},
		"issue.link": map[string]any{
			"type":        "object",
			"description": "issue link --json-input payload: the exact POST /rest/api/3/issueLink body. inwardIssue may come from the payload or the positional KEY; setting both to different values is an error. The optional comment block's ADF body is validated before submission.",
			"required":    []string{"type", "outwardIssue"},
			"properties": map[string]any{
				"type":         map[string]any{"type": "object", "description": "Link type by name or id: {\"name\": \"Blocks\"} or {\"id\": \"10003\"}."},
				"inwardIssue":  map[string]any{"type": "object", "description": "{\"key\": ...}; optional when the KEY argument supplies it."},
				"outwardIssue": map[string]any{"type": "object", "description": "{\"key\": ...}."},
				"comment":      map[string]any{"type": "object", "description": "Native comment block; its body is a canonical ADF document."},
			},
		},
		"issue.comment":      commentBody,
		"issue.comment.add":  commentBody,
		"issue.comment.edit": commentBody,
		"worklog.add": map[string]any{
			"type":        "object",
			"description": "worklog add --json-input payload.",
			"properties": map[string]any{
				"time_spent":       map[string]any{"type": "string"},
				"started":          map[string]any{"type": "string"},
				"comment":          map[string]any{"$ref": "#/data/input_schemas/adf_document"},
				"comment_markdown": map[string]any{"type": "string", "description": "Markdown converted to ADF; mutually exclusive with comment."},
			},
		},
	}
}

// withDerivedSchemas swaps every hand-written entry whose operation has a
// typed Output registered (internal/envelope) for a schema derived from the
// struct itself, carrying the literal's prose (descriptions, enums, formats)
// forward as overrides. Family conversions therefore never edit this file:
// registering the struct is the whole schema change, and the shape can no
// longer disagree with what the builder emits.
func withDerivedSchemas(schemas map[string]any) map[string]any {
	for op, v := range envelope.Outputs() {
		if _, dynamic := v.(envelope.Dynamic); dynamic {
			continue // shape is hand-written by documented exception
		}
		overrides, _ := proseOverrides(schemas[op]).(map[string]any)
		if overrides == nil {
			overrides = map[string]any{}
		}
		if doc := envelope.Doc(op); doc != nil {
			overrides = mergeAnyMaps(overrides, doc)
		}
		schemas[op] = envelope.SchemaOf(v, overrides)
	}
	return schemas
}

// mergeAnyMaps deep-merges b onto a and returns a; map values merge
// recursively, anything else in b replaces a's value.
func mergeAnyMaps(a, b map[string]any) map[string]any {
	for k, bv := range b {
		if am, aIsMap := a[k].(map[string]any); aIsMap {
			if bm, bIsMap := bv.(map[string]any); bIsMap {
				a[k] = mergeAnyMaps(am, bm)
				continue
			}
		}
		a[k] = bv
	}
	return a
}

// proseOverrides strips a hand-written schema down to the keys the type
// system cannot express — description, enum, format — preserving the
// properties/items nesting so the prose lands on the right derived field.
// Shape keys (type, required) are dropped: the struct owns them now.
func proseOverrides(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]any{}
	for _, key := range []string{"description", "enum", "format"} {
		if val, present := m[key]; present {
			out[key] = val
		}
	}
	if props, isMap := m["properties"].(map[string]any); isMap {
		sub := map[string]any{}
		for name, ps := range props {
			if p := proseOverrides(ps); p != nil {
				sub[name] = p
			}
		}
		if len(sub) > 0 {
			out["properties"] = sub
		}
	}
	if items, present := m["items"]; present {
		if nested := proseOverrides(items); nested != nil {
			out["items"] = nested
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
