Add a rule to unexpanded for: $ARGUMENTS

1. Find the kubelet code that decides the behavior, and link it in the PR. Find a real manifest or bug report where it matters, and say where it came from.
2. Add it in `plugin/scripts/check.go`, with a code in kebab case, a message that says what kubelet will do, and a fix line.
3. Add a case to `TestRules` in `plugin/scripts/unexpanded_test.go`, plus a case that must stay quiet. Check that `tests/testdata/real` stays quiet.
4. Add the code to the table in `README.md` and to the skill's list in `plugin/skills/checking-kubernetes-env-references/SKILL.md`.
5. Run `make test`.
