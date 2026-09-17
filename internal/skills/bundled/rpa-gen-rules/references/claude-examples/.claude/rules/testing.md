---
paths:
  - "tests/**/*.py"
---

# Testing conventions

This rule uses **path-specific** frontmatter so it loads when Claude works under `tests/`. See [path-specific rules](https://code.claude.com/docs/en/memory#path-specific-rules).

## Principles

1. Test names describe behavior (`test_<behavior>`).
2. Prefer Arrange / Act / Assert.
3. Keep tests independent. Use fixtures for shared setup.
4. Match project pytest config (`pytest.ini`, `pyproject.toml`).

## Layout

- Files: `tests/test_<module>.py`
- Classes: `Test<Subject>`
- Run all: `.venv/bin/pytest tests/ -v`
- Coverage (if used): `pytest tests/ --cov=<package> --cov-report=term-missing`

## Example

```python
def test_returns_empty_when_missing(search_service):
    result = search_service.find("missing-key")

    assert result.is_empty()
```
