package cmd

import (
	"fmt"
	"strings"

	"github.com/csams/todo/store"
	"github.com/spf13/cobra"
)

var noteCmd = &cobra.Command{
	Use:   "note",
	Short: "Manage notes",
}

var noteAddCmd = &cobra.Command{
	Use:   "add [text...]",
	Short: "Add a note (use --task to attach to a task; otherwise creates a standalone note)",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close(cmd.Context())

		taskFlag, _ := cmd.Flags().GetUint("task")
		var taskID *uint
		if taskFlag > 0 {
			t := taskFlag
			taskID = &t
		}
		text := strings.Join(args, " ")

		note, err := s.AddNote(cmd.Context(), taskID, text)
		if err != nil {
			return err
		}

		if jsonOutput {
			return outputJSON(note)
		}
		if note.TaskID != nil {
			fmt.Printf("Added note #%d to task %d\n", note.ID, *note.TaskID)
		} else {
			fmt.Printf("Added standalone note #%d\n", note.ID)
		}
		return nil
	},
}

var noteUpdateCmd = &cobra.Command{
	Use:   "update <note-id>",
	Short: "Update a note's text, parent task, or archived flag",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close(cmd.Context())

		noteID, err := parseUint(args[0])
		if err != nil {
			return err
		}

		opts := store.UpdateNoteOptions{}
		if cmd.Flags().Changed("text") {
			t, _ := cmd.Flags().GetString("text")
			opts.Text = &t
		}
		if cmd.Flags().Changed("task") {
			t, _ := cmd.Flags().GetUint("task")
			opts.SetTaskID = true
			opts.TaskID = &t
		}
		if clear, _ := cmd.Flags().GetBool("clear-task"); clear {
			if opts.SetTaskID {
				return fmt.Errorf("--task and --clear-task are mutually exclusive")
			}
			opts.SetTaskID = true
			opts.TaskID = nil
		}
		archive, _ := cmd.Flags().GetBool("archive")
		unarchive, _ := cmd.Flags().GetBool("unarchive")
		if archive && unarchive {
			return fmt.Errorf("--archive and --unarchive are mutually exclusive")
		}
		if archive {
			b := true
			opts.Archived = &b
		} else if unarchive {
			b := false
			opts.Archived = &b
		}

		if opts.Text == nil && !opts.SetTaskID && opts.Archived == nil {
			return fmt.Errorf("provide at least one of --text, --task, --clear-task, --archive, --unarchive")
		}

		note, err := s.UpdateNote(cmd.Context(), noteID, opts)
		if err != nil {
			return err
		}

		if jsonOutput {
			return outputJSON(note)
		}
		fmt.Printf("Updated note #%d\n", note.ID)
		return nil
	},
}

var noteListCmd = &cobra.Command{
	Use:   "list [task-id]",
	Short: "List notes (no args: all; --standalone: only standalone; <task-id>: that task's notes). Archived notes are excluded unless --include-archived is set.",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close(cmd.Context())

		all, _ := cmd.Flags().GetBool("all")
		standalone, _ := cmd.Flags().GetBool("standalone")
		includeArchived, _ := cmd.Flags().GetBool("include-archived")

		opts := store.ListNotesOptions{IncludeArchived: includeArchived}
		switch {
		case len(args) == 1:
			taskID, err := parseTaskID(args[0])
			if err != nil {
				return err
			}
			opts.TaskID = &taskID
		case standalone:
			opts.Scope = store.NoteScopeStandalone
		case all:
			opts.Scope = store.NoteScopeAll
		default:
			// No positional, no flag: list everything (least surprising default).
			opts.Scope = store.NoteScopeAll
		}
		notes, err := s.ListNotes(cmd.Context(), opts)
		if err != nil {
			return err
		}
		return outputNotes(notes)
	},
}

var noteDeleteCmd = &cobra.Command{
	Use:   "delete <note-id>",
	Short: "Delete a note",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close(cmd.Context())

		noteID, err := parseUint(args[0])
		if err != nil {
			return err
		}

		if err := s.DeleteNote(cmd.Context(), noteID); err != nil {
			return err
		}

		fmt.Printf("Deleted note %d\n", noteID)
		return nil
	},
}

var noteArchiveCmd = &cobra.Command{
	Use:   "archive <note-id>",
	Short: "Archive a note",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close(cmd.Context())
		id, err := parseUint(args[0])
		if err != nil {
			return err
		}
		if err := s.ArchiveNote(cmd.Context(), id, true); err != nil {
			return err
		}
		fmt.Printf("Archived note %d\n", id)
		return nil
	},
}

var noteUnarchiveCmd = &cobra.Command{
	Use:   "unarchive <note-id>",
	Short: "Unarchive a note",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close(cmd.Context())
		id, err := parseUint(args[0])
		if err != nil {
			return err
		}
		if err := s.ArchiveNote(cmd.Context(), id, false); err != nil {
			return err
		}
		fmt.Printf("Unarchived note %d\n", id)
		return nil
	},
}

var noteSearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search notes by text content",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close(cmd.Context())

		includeArchived, _ := cmd.Flags().GetBool("include-archived")
		notes, err := s.ListNotes(cmd.Context(), store.ListNotesOptions{
			Query:           args[0],
			IncludeArchived: includeArchived,
		})
		if err != nil {
			return err
		}

		return outputNotes(notes)
	},
}

func init() {
	noteAddCmd.Flags().Uint("task", 0, "attach the note to this task ID (omit for standalone)")

	noteUpdateCmd.Flags().String("text", "", "new note text")
	noteUpdateCmd.Flags().Uint("task", 0, "reparent the note to this task ID")
	noteUpdateCmd.Flags().Bool("clear-task", false, "detach the note from any task (make standalone)")
	noteUpdateCmd.Flags().Bool("archive", false, "set archived=true")
	noteUpdateCmd.Flags().Bool("unarchive", false, "set archived=false")

	noteListCmd.Flags().Bool("all", false, "list every note (attached + standalone)")
	noteListCmd.Flags().Bool("standalone", false, "list only standalone notes")
	noteListCmd.Flags().Bool("include-archived", false, "include archived notes in results (default: false)")

	noteSearchCmd.Flags().Bool("include-archived", false, "include archived notes in results (default: false)")

	noteCmd.AddCommand(noteAddCmd)
	noteCmd.AddCommand(noteUpdateCmd)
	noteCmd.AddCommand(noteListCmd)
	noteCmd.AddCommand(noteDeleteCmd)
	noteCmd.AddCommand(noteArchiveCmd)
	noteCmd.AddCommand(noteUnarchiveCmd)
	noteCmd.AddCommand(noteSearchCmd)
	rootCmd.AddCommand(noteCmd)
}
