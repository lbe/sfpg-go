# Architecture Diagrams

This directory contains Mermaid diagrams for the SFPG photo gallery application.

## Files

- **ARCHITECTURE_DIAGRAMS.md** - Complete set of system architecture diagrams
- **MERMAID_QUICK_START.md** - Guide for creating and editing Mermaid diagrams

## How to View

### GitHub/GitLab (Easiest)

Simply open these files on GitHub or GitLab - they render Mermaid diagrams natively.

### VS Code

1. Install "Markdown Preview Mermaid Support" extension
2. Open any `.md` file
3. Press `Ctrl+Shift+V` (or `Cmd+Shift+V`)

### Online Editor

Visit https://mermaid.live/ and paste diagram code

## Included Diagrams

1. **System Overview** - High-level component architecture
2. **Request Flow** - How HTTP requests flow through the system
3. **Authentication Flow** - Login and session management
4. **File Processing Pipeline** - Image discovery and processing
5. **Cache Architecture** - HTTP cache with preload and eviction
6. **Database Architecture** - Connection pools and schema
7. **Configuration Flow** - Config loading and persistence
8. **Component Dependencies** - Package dependency graph

## Contributing

When adding new diagrams:

1. Keep them focused on one aspect
2. Use consistent styling
3. Test rendering in GitHub before committing
4. Update this README

When code changes:

- Update related diagrams if the architecture changes
- Add new diagrams for new major components

## Related Documentation

- [Server Architecture](../../internal/server/ARCHITECTURE.md)
- [Interface Designs](../../Interface-Designs.md)
- [Main README](../../README.md)
