# Example scheduled tasks

Three tasks matching a quarter-hourly check, an hourly tidy and a daily digest. They do nothing
here: crowsnest reads task files only from `<orchestrator dir>/tasks/` and
`~/.config/crowsnest/tasks/`. Copy the ones you want there (and edit them):

```sh
mkdir -p ~/.config/crowsnest/tasks
cp examples/tasks/check.md examples/tasks/digest.md ~/.config/crowsnest/tasks/
```

The file format is in the main README under "Scheduled work". Each file can also be switched off
in the Schedule view without deleting it.
