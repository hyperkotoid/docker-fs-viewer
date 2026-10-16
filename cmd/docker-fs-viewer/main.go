package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/denis/docker-fs-viewer/internal/app"
	"github.com/denis/docker-fs-viewer/internal/model"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		usage()
		return nil
	}
	application, err := app.New(ctx)
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		return list(ctx, application)
	case "sessions":
		return sessions(application)
	case "mount":
		if len(args) != 2 {
			return errors.New("usage: docker-fs-viewer mount CONTAINER")
		}
		return mount(ctx, application, args[1])
	case "status":
		ref := optionalRef(args[1:])
		return status(ctx, application, ref)
	case "unmount":
		flags := flag.NewFlagSet("unmount", flag.ContinueOnError)
		force := flags.Bool("force", false, "force unmount")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		item, err := application.Unmount(ctx, optionalRef(flags.Args()), *force)
		if err != nil {
			return err
		}
		fmt.Printf("Unmounted session %s. Workspace is preserved.\n", item.ID)
		return nil
	case "apply":
		flags := flag.NewFlagSet("apply", flag.ContinueOnError)
		yes := flags.Bool("yes", false, "apply without interactive confirmation")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		return applyChanges(ctx, application, optionalRef(flags.Args()), *yes)
	case "rollback":
		flags := flag.NewFlagSet("rollback", flag.ContinueOnError)
		yes := flags.Bool("yes", false, "rollback without interactive confirmation")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		return rollback(ctx, application, optionalRef(flags.Args()), *yes)
	default:
		return fmt.Errorf("unknown command %q (run with help)", args[0])
	}
}

func list(ctx context.Context, application *app.App) error {
	containers, err := application.List(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("%-14s %-28s %-10s %s\n", "ID", "NAME", "STATE", "IMAGE")
	for _, item := range containers {
		name := strings.TrimPrefix(first(item.Names), "/")
		fmt.Printf("%-14s %-28s %-10s %s\n", short(item.ID), name, item.State, item.Image)
	}
	return nil
}

func sessions(application *app.App) error {
	items, err := application.SessionsList()
	if err != nil {
		return err
	}
	fmt.Printf("%-32s %-24s %-12s %s\n", "SESSION", "CONTAINER", "STATE", "MOUNT")
	for _, item := range items {
		fmt.Printf("%-32s %-24s %-12s %s\n", item.ID, strings.TrimPrefix(item.ContainerName, "/"), item.State, item.MountPoint)
	}
	return nil
}

func mount(ctx context.Context, application *app.App, container string) error {
	result, err := application.Mount(ctx, container)
	if err != nil {
		return err
	}
	fmt.Printf("Mounted %s at %s\n", result.Session.ID, result.Session.MountPoint)
	if len(result.ExternalMounts) > 0 {
		fmt.Println("External mounts are visible only as underlying rootfs paths and will not be modified:")
		for _, item := range result.ExternalMounts {
			fmt.Printf("  %s %s -> %s\n", item.Type, item.Source, item.Destination)
		}
	}
	if result.FinderWarning != nil {
		fmt.Fprintf(os.Stderr, "warning: mounted successfully, but Finder could not be opened: %v\n", result.FinderWarning)
	}
	fmt.Printf("Edit files in Finder, then run: docker-fs-viewer status %s\n", result.Session.ID)
	return nil
}

func status(ctx context.Context, application *app.App, ref string) error {
	item, changes, err := application.Status(ctx, ref)
	if err != nil {
		return err
	}
	fmt.Printf("Session %s (%s): %d change(s)\n", item.ID, strings.TrimPrefix(item.ContainerName, "/"), len(changes))
	printChanges(changes)
	return nil
}

func applyChanges(ctx context.Context, application *app.App, ref string, yes bool) error {
	item, changes, err := application.Status(ctx, ref)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		return errors.New("workspace has no changes")
	}
	fmt.Printf("Apply %d change(s) to %s:\n", len(changes), strings.TrimPrefix(item.ContainerName, "/"))
	printChanges(changes)
	fmt.Println("The stopped container will be renamed as a backup; a replacement will receive its original name and remain stopped.")
	if !yes && !confirm("Continue? [y/N] ") {
		return errors.New("apply cancelled")
	}
	item, _, err = application.Apply(ctx, item.ID)
	if err != nil {
		return err
	}
	fmt.Printf("Applied. Replacement: %s; backup: %s\n", short(item.ReplacementID), item.BackupName)
	fmt.Printf("Rollback: docker-fs-viewer rollback %s\n", item.ID)
	return nil
}

func rollback(ctx context.Context, application *app.App, ref string, yes bool) error {
	item, err := application.ResolveSession(ref)
	if err != nil {
		return err
	}
	if item.ReplacementID == "" {
		return errors.New("session has no applied replacement")
	}
	if !yes && !confirm("Remove the stopped replacement and restore the backup? [y/N] ") {
		return errors.New("rollback cancelled")
	}
	item, err = application.Rollback(ctx, item.ID)
	if err != nil {
		return err
	}
	fmt.Printf("Restored original container %s\n", strings.TrimPrefix(item.ContainerName, "/"))
	return nil
}

func printChanges(changes []model.Change) {
	for _, change := range changes {
		fmt.Printf("  %-8s %s\n", change.Kind, change.Path)
	}
}

func confirm(prompt string) bool {
	fmt.Print(prompt)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

func optionalRef(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func short(value string) string {
	if len(value) > 12 {
		return value[:12]
	}
	return value
}

func usage() {
	fmt.Print(`Docker FS Viewer mounts a stopped Docker Desktop container as an editable APFS volume.

Usage:
  docker-fs-viewer list
  docker-fs-viewer sessions
  docker-fs-viewer mount CONTAINER
  docker-fs-viewer status [SESSION]
  docker-fs-viewer apply [--yes] [SESSION]
  docker-fs-viewer unmount [--force] [SESSION]
  docker-fs-viewer rollback [--yes] [SESSION]
`)
}
