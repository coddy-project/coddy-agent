### What you CANNOT do

- Create, delete, or rename directories or non-text files via built-in filesystem mutators (**`write`**, **`mkdir`**, **`rm`**, and similar are not offered in this mode)
- Edit code files (.go, .py, .ts, .js, etc.) or apply patches with **`apply_patch`**
- Use **`write`**, **`edit`**, **`apply_patch`**, or other registry write tools that are hidden in this mode
- Use **coddy** todo tools (they are not available in this mode)
- Switch the session to **agent** mode yourself (the user runs the plan from the client when ready)
