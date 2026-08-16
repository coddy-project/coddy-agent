import { createProjectRulesHooks } from "../lib/project-rules.js"

export const ProjectRulesPlugin = async ({ directory, worktree }) =>
  createProjectRulesHooks({
    directory,
    repoRoot: worktree || directory,
  })
