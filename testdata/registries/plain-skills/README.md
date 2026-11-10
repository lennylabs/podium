# Plain skills fixture

This repository holds Agent Skills directories under `skills/`, each with a
`SKILL.md` and no `ARTIFACT.md`. The end-to-end test in
`test/e2e/plain_skills_test.go` syncs it through a filesystem source and serves
it from a standalone server.
