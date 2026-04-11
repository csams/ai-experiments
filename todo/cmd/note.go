package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var noteCmd = &cobra.Command{
	Use:   "note",
	Short: "Manage notes",
}

var noteAddCmd = &cobra.Command{
	Use:   "add <task-id> <text>",
	Short: "Add a note to a task",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close()

		taskID, err := parseTaskID(args[0])
		if err != nil {
			return err
		}
		text := strings.Join(args[1:], " ")

		note, err := s.AddNote(taskID, text)
		if err != nil {
			return err
		}

		if jsonOutput {
			return outputJSON(note)
		}
		fmt.Printf("Added note #%d to task %d\n", note.ID, taskID)
		return nil
	},
}

var noteUpdateCmd = &cobra.Command{
	Use:   "update <task-id> <note-id> <text>",
	Short: "Update a note's text",
	Args:  cobra.MinimumNArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close()

		taskID, err := parseTaskID(args[0])
		if err != nil {
			return err
		}
		noteID, err := parseUint(args[1])
		if err != nil {
			return err
		}
		text := strings.Join(args[2:], " ")

		note, err := s.UpdateNote(taskID, noteID, text)
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
	Use:   "list <task-id>",
	Short: "List notes for a task",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close()

		taskID, err := parseTaskID(args[0])
		if err != nil {
			return err
		}

		notes, err := s.ListNotes(taskID)
		if err != nil {
			return err
		}

		outputNotes(notes)
		return nil
	},
}

var noteDeleteCmd = &cobra.Command{
	Use:   "delete <task-id> <note-id>",
	Short: "Delete a note",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close()

		taskID, err := parseTaskID(args[0])
		if err != nil {
			return err
		}
		noteID, err := parseUint(args[1])
		if err != nil {
			return err
		}

		if err := s.DeleteNote(taskID, noteID); err != nil {
			return err
		}

		fmt.Printf("Deleted note %d from task %d\n", noteID, taskID)
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
		defer s.Close()

		notes, err := s.SearchNotes(args[0])
		if err != nil {
			return err
		}

		outputNotes(notes)
		return nil
	},
}

func init() {
	noteCmd.AddCommand(noteAddCmd)
	noteCmd.AddCommand(noteUpdateCmd)
	noteCmd.AddCommand(noteListCmd)
	noteCmd.AddCommand(noteDeleteCmd)
	noteCmd.AddCommand(noteSearchCmd)
	rootCmd.AddCommand(noteCmd)
}
