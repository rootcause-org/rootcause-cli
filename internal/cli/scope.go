package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// scopeSpec is the fail-closed scope contract for one executable command. Project and Tenant mean
// that the command actively applies the corresponding persistent selector; AllProject means its own
// --all flag is supported. Commands not listed by commandScope default to rejecting every selector.
type scopeSpec struct {
	Project     bool
	Tenant      bool
	AllProjects bool
}

const scopeAnnotation = "rootcause.dev/scope"

// annotateCommandScopes stamps every executable node once the tree is assembled. Keeping this
// cross-cutting contract here prevents persistent selectors from becoming silently accepted by a new
// command: the safe default is rejection until commandScope explicitly opts the path in.
func annotateCommandScopes(root *cobra.Command) {
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd.Run != nil || cmd.RunE != nil {
			spec := commandScope(cmd.CommandPath())
			if cmd.Annotations == nil {
				cmd.Annotations = map[string]string{}
			}
			cmd.Annotations[scopeAnnotation] = encodeScopeSpec(spec)
		}
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(root)
}

func encodeScopeSpec(spec scopeSpec) string {
	return fmt.Sprintf("project=%t,tenant=%t,all=%t", spec.Project, spec.Tenant, spec.AllProjects)
}

func decodeScopeSpec(value string) (scopeSpec, error) {
	var spec scopeSpec
	if _, err := fmt.Sscanf(value, "project=%t,tenant=%t,all=%t", &spec.Project, &spec.Tenant, &spec.AllProjects); err != nil {
		return scopeSpec{}, fmt.Errorf("invalid command scope metadata %q: %w", value, err)
	}
	return spec, nil
}

func commandScope(path string) scopeSpec {
	path = strings.TrimSpace(strings.TrimPrefix(path, "rc "))
	projectTenant := scopeSpec{Project: true, Tenant: true}
	projectOnly := scopeSpec{Project: true}

	// Tenant-capable runtime and inherited-resource surfaces. Positional tenant-record commands are
	// deliberately excluded: their slug argument is the target, never ambient --tenant context.
	switch {
	case path == "status", path == "ask":
		return projectTenant
	case path == "run" || strings.HasPrefix(path, "run "):
		return projectTenant
	case strings.HasPrefix(path, "fleet "):
		return scopeSpec{Project: true, Tenant: true, AllProjects: true}
	case strings.HasPrefix(path, "project triage "), strings.HasPrefix(path, "project senders "):
		return projectTenant
	case strings.HasPrefix(path, "project repo "), strings.HasPrefix(path, "project connection "), strings.HasPrefix(path, "project member "):
		return projectTenant
	case strings.HasPrefix(path, "project mailbox ") &&
		!strings.HasPrefix(path, "project mailbox settings ") &&
		!strings.HasPrefix(path, "project mailbox route "):
		return projectTenant
	case strings.HasPrefix(path, "project corpus "), strings.HasPrefix(path, "project env "):
		return projectTenant
	case strings.HasPrefix(path, "project knowledge content "):
		return projectTenant
	case strings.HasPrefix(path, "dev brain "):
		return projectTenant
	case strings.HasPrefix(path, "dev console database "), strings.HasPrefix(path, "dev console bash "), path == "dev console capabilities":
		return projectTenant
	case path == "dev learning evidence", path == "auth status":
		return projectTenant
	}

	// Project-owned configuration deliberately rejects tenant selection. Tenant profile/settings take
	// an explicit positional slug, so ambient tenant context cannot redirect those writes.
	switch {
	case path == "project rename", strings.HasPrefix(path, "project settings "):
		return projectOnly
	case strings.HasPrefix(path, "project tenant "):
		return projectOnly
	case strings.HasPrefix(path, "project mailbox settings "):
		return projectOnly
	case strings.HasPrefix(path, "project mailbox route "):
		return projectOnly
	case strings.HasPrefix(path, "project model-key "), strings.HasPrefix(path, "project knowledge sync "):
		return projectOnly
	case strings.HasPrefix(path, "project database "), strings.HasPrefix(path, "project token "):
		return projectOnly
	case strings.HasPrefix(path, "project branding "), strings.HasPrefix(path, "project github "), strings.HasPrefix(path, "project action-settings "):
		return projectOnly
	case strings.HasPrefix(path, "dev console action "), path == "auth access":
		return projectOnly
	}

	return scopeSpec{} // project list, admin, auth login/logout, dev API/tools, and self are unscoped.
}

func validateCommandScope(e *env, cmd *cobra.Command) error {
	if cmd.Annotations == nil || cmd.Annotations[scopeAnnotation] == "" {
		return fmt.Errorf("internal error: `%s` has no scope contract", cmd.CommandPath())
	}
	spec, err := decodeScopeSpec(cmd.Annotations[scopeAnnotation])
	if err != nil {
		return err
	}
	if e.project != "" && !spec.Project {
		return fmt.Errorf("--project is not supported by `%s`", cmd.CommandPath())
	}
	if e.tenant != "" && !spec.Tenant {
		return fmt.Errorf("--tenant is not supported by `%s`", cmd.CommandPath())
	}
	if cmd.Flags().Lookup("all") != nil && cmd.Flags().Changed("all") {
		all, err := cmd.Flags().GetBool("all")
		if err != nil {
			return err
		}
		if all {
			if !spec.AllProjects {
				return fmt.Errorf("--all is not supported by `%s`", cmd.CommandPath())
			}
			if e.project != "" || e.tenant != "" {
				return fmt.Errorf("--all cannot be combined with --project or --tenant")
			}
		}
	}
	return nil
}

// installScopeHeader prefixes human output lazily, after newClient has resolved any brain/login project
// needed by an explicit tenant. JSON remains the byte-faithful server response.
func installScopeHeader(e *env, cmd *cobra.Command) {
	if e.jsonOut() || cmd.Annotations == nil {
		return
	}
	spec, err := decodeScopeSpec(cmd.Annotations[scopeAnnotation])
	if err != nil || !spec.Tenant {
		return
	}
	if _, wrapped := e.out.(*scopeHeaderWriter); wrapped {
		return
	}
	e.scopeHeader = true
	e.out = &scopeHeaderWriter{dst: e.out, label: func() string {
		tenant := e.scopeTenant()
		if tenant == "" {
			return ""
		}
		if project := e.scopeProject(); project != "" {
			return project + " / " + tenant
		}
		return tenant
	}}
}

type scopeHeaderWriter struct {
	dst   io.Writer
	label func() string
	wrote bool
}

// UnwrapWriter preserves auto TTY detection after the human-only prefix decorator is installed.
func (w *scopeHeaderWriter) UnwrapWriter() io.Writer { return w.dst }

func (w *scopeHeaderWriter) Write(p []byte) (int, error) {
	if !w.wrote {
		w.wrote = true
		if label := w.label(); label != "" {
			if _, err := fmt.Fprintf(w.dst, "Scope: %s\n", label); err != nil {
				return 0, err
			}
		}
	}
	return w.dst.Write(p)
}
