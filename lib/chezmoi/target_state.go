package chezmoi

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	vfs "github.com/twpayne/go-vfs"
)

// A TargetState represents the root target state.
type TargetState struct {
	TargetDir string
	Umask     os.FileMode
	SourceDir string
	Data      map[string]interface{}
	Funcs     template.FuncMap
	Entries   map[string]Entry
}

// NewTargetState creates a new TargetState.
func NewTargetState(targetDir string, umask os.FileMode, sourceDir string, data map[string]interface{}, funcs template.FuncMap) *TargetState {
	return &TargetState{
		TargetDir: targetDir,
		Umask:     umask,
		SourceDir: sourceDir,
		Data:      data,
		Funcs:     funcs,
		Entries:   make(map[string]Entry),
	}
}

// Add adds a new target to ts.
func (ts *TargetState) Add(fs vfs.FS, targetPath string, info os.FileInfo, addEmpty, addTemplate bool, mutator Mutator) error {
	if !filepath.HasPrefix(targetPath, ts.TargetDir) {
		return fmt.Errorf("%s: outside target directory", targetPath)
	}
	targetName, err := filepath.Rel(ts.TargetDir, targetPath)
	if err != nil {
		return err
	}
	if info == nil {
		var err error
		info, err = fs.Lstat(targetPath)
		if err != nil {
			return err
		}
	}

	// Add the parent directories, if needed.
	parentDirSourceName := ""
	entries := ts.Entries
	if parentDirName := filepath.Dir(targetName); parentDirName != "." {
		parentEntry, err := ts.findEntry(parentDirName)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if parentEntry == nil {
			if err := ts.Add(fs, filepath.Join(ts.TargetDir, parentDirName), nil, false, false, mutator); err != nil {
				return err
			}
			parentEntry, err = ts.findEntry(parentDirName)
			if err != nil {
				return err
			}
		} else if _, ok := parentEntry.(*Dir); !ok {
			return fmt.Errorf("%s: not a directory", parentDirName)
		}
		parentDir := parentEntry.(*Dir)
		parentDirSourceName = parentDir.sourceName
		entries = parentDir.Entries
	}

	switch {
	case info.Mode().IsDir():
		perm := info.Mode().Perm()
		infos, err := fs.ReadDir(targetPath)
		if err != nil {
			return err
		}
		empty := len(infos) == 0
		return ts.addDir(targetName, entries, parentDirSourceName, perm, empty, mutator)
	case info.Mode().IsRegular():
		perm := info.Mode().Perm()
		empty := info.Size() == 0
		if empty && !addEmpty {
			return nil
		}
		contents, err := fs.ReadFile(targetPath)
		if err != nil {
			return err
		}
		if addTemplate {
			contents = autoTemplate(contents, ts.Data)
		}
		// FIXME refactor to pass info instead of perm and empty
		return ts.addFile(targetName, entries, parentDirSourceName, perm, empty, addTemplate, contents, mutator)
	case info.Mode()&os.ModeType == os.ModeSymlink:
		linkName, err := fs.Readlink(targetPath)
		if err != nil {
			return err
		}
		return ts.addSymlink(targetName, entries, parentDirSourceName, linkName, mutator)
	default:
		return fmt.Errorf("%s: not a regular file, directory, or symlink", targetName)
	}
	return nil
}

// Archive writes ts to w.
func (ts *TargetState) Archive(w *tar.Writer, umask os.FileMode) error {
	currentUser, err := user.Current()
	if err != nil {
		return err
	}
	uid, err := strconv.Atoi(currentUser.Uid)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(currentUser.Gid)
	if err != nil {
		return err
	}
	group, err := user.LookupGroupId(currentUser.Gid)
	if err != nil {
		return err
	}
	now := time.Now()
	headerTemplate := tar.Header{
		Uid:        uid,
		Gid:        gid,
		Uname:      currentUser.Username,
		Gname:      group.Name,
		ModTime:    now,
		AccessTime: now,
		ChangeTime: now,
	}
	for _, entryName := range sortedEntryNames(ts.Entries) {
		if err := ts.Entries[entryName].archive(w, &headerTemplate, umask); err != nil {
			return err
		}
	}
	return nil
}

// Apply ensures that ts.TargetDir in fs matches ts.
func (ts *TargetState) Apply(fs vfs.FS, mutator Mutator) error {
	for _, entryName := range sortedEntryNames(ts.Entries) {
		if err := ts.Entries[entryName].Apply(fs, ts.TargetDir, ts.Umask, mutator); err != nil {
			return err
		}
	}
	return nil
}

// ConcreteValue returns a value suitable for serialization.
func (ts *TargetState) ConcreteValue(recursive bool) (interface{}, error) {
	var entryConcreteValues []interface{}
	for _, entryName := range sortedEntryNames(ts.Entries) {
		entryConcreteValue, err := ts.Entries[entryName].ConcreteValue(ts.TargetDir, ts.SourceDir, recursive)
		if err != nil {
			return nil, err
		}
		entryConcreteValues = append(entryConcreteValues, entryConcreteValue)
	}
	return entryConcreteValues, nil
}

// Evaluate evaluates all of the entries in ts.
func (ts *TargetState) Evaluate() error {
	for _, entryName := range sortedEntryNames(ts.Entries) {
		if err := ts.Entries[entryName].Evaluate(); err != nil {
			return err
		}
	}
	return nil
}

// Get returns the state of the given target, or nil if no such target is found.
func (ts *TargetState) Get(target string) (Entry, error) {
	if !filepath.HasPrefix(target, ts.TargetDir) {
		return nil, fmt.Errorf("%s: outside target directory", target)
	}
	targetName, err := filepath.Rel(ts.TargetDir, target)
	if err != nil {
		return nil, err
	}
	return ts.findEntry(targetName)
}

// Import imports an archive.
func (ts *TargetState) Import(r *tar.Reader, destinationDir string, stripComponents int, mutator Mutator) error {
	for {
		header, err := r.Next()
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir, tar.TypeReg, tar.TypeSymlink:
			if err := ts.importHeader(r, header, destinationDir, stripComponents, mutator); err != nil {
				return err
			}
		case tar.TypeXGlobalHeader:
		default:
			return fmt.Errorf("%s: unspported typeflag '%c'", header.Name, header.Typeflag)
		}
	}
	return nil
}

// Populate walks fs from ts.SourceDir to populate ts.
func (ts *TargetState) Populate(fs vfs.FS) error {
	return vfs.Walk(fs, ts.SourceDir, func(path string, info os.FileInfo, _ error) error {
		relPath, err := filepath.Rel(ts.SourceDir, path)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil
		}
		// Ignore all files and directories beginning with "."
		if _, name := filepath.Split(relPath); strings.HasPrefix(name, ".") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case info.Mode().IsDir():
			components := splitPathList(relPath)
			dirNames, perms := parseDirNameComponents(components)
			targetName := filepath.Join(dirNames...)
			entries, err := ts.findEntries(dirNames[:len(dirNames)-1])
			if err != nil {
				return err
			}
			dirName := dirNames[len(dirNames)-1]
			perm := perms[len(perms)-1]
			entries[dirName] = newDir(relPath, targetName, perm)
		case info.Mode().IsRegular():
			psfp := parseSourceFilePath(relPath)
			entries, err := ts.findEntries(psfp.dirNames)
			if err != nil {
				return err
			}

			targetName := filepath.Join(append(psfp.dirNames, psfp.FileName)...)
			var entry Entry
			switch psfp.Mode & os.ModeType {
			case 0:
				evaluateContents := func() ([]byte, error) {
					return fs.ReadFile(path)
				}
				if psfp.Template {
					evaluateContents = func() ([]byte, error) {
						return ts.executeTemplate(fs, path)
					}
				}
				entry = &File{
					sourceName:       relPath,
					targetName:       targetName,
					Empty:            psfp.Empty,
					Perm:             psfp.Mode & os.ModePerm,
					Template:         psfp.Template,
					evaluateContents: evaluateContents,
				}
			case os.ModeSymlink:
				evaluateLinkName := func() (string, error) {
					data, err := fs.ReadFile(path)
					return string(data), err
				}
				if psfp.Template {
					evaluateLinkName = func() (string, error) {
						data, err := ts.executeTemplate(fs, path)
						return string(data), err
					}
				}
				entry = &Symlink{
					sourceName:       relPath,
					targetName:       targetName,
					Template:         psfp.Template,
					evaluateLinkName: evaluateLinkName,
				}
			default:
				return fmt.Errorf("%v: unsupported mode: %d", path, psfp.Mode&os.ModeType)
			}
			entries[psfp.FileName] = entry
		default:
			return fmt.Errorf("unsupported file type: %s", path)
		}
		return nil
	})
}

func (ts *TargetState) addDir(targetName string, entries map[string]Entry, parentDirSourceName string, perm os.FileMode, empty bool, mutator Mutator) error {
	name := filepath.Base(targetName)
	var existingDir *Dir
	if entry, ok := entries[name]; ok {
		existingDir, ok = entry.(*Dir)
		if !ok {
			return fmt.Errorf("%s: already added and not a directory", targetName)
		}
	}
	sourceName := ParsedSourceDirName{
		DirName: name,
		Perm:    perm,
	}.SourceDirName()
	if parentDirSourceName != "" {
		sourceName = filepath.Join(parentDirSourceName, sourceName)
	}
	dir := newDir(sourceName, targetName, perm)
	if existingDir != nil {
		if existingDir.sourceName == dir.sourceName {
			return nil
		}
		return mutator.Rename(filepath.Join(ts.SourceDir, existingDir.sourceName), filepath.Join(ts.SourceDir, dir.sourceName))
	}
	// If the directory is empty, add a .keep file so the directory is
	// managed by git. Chezmoi will ignore the .keep file as it begins with
	// a dot.
	if empty {
		if err := mutator.WriteFile(filepath.Join(ts.SourceDir, sourceName, ".keep"), nil, 0666&^ts.Umask, nil); err != nil {
			return err
		}
	}
	entries[name] = dir
	return mutator.Mkdir(filepath.Join(ts.SourceDir, sourceName), 0777&^ts.Umask)
}

func (ts *TargetState) addFile(targetName string, entries map[string]Entry, parentDirSourceName string, perm os.FileMode, empty bool, template bool, contents []byte, mutator Mutator) error {
	// FIXME refactor to take an os.FileMode instead of perm and empty
	name := filepath.Base(targetName)
	var existingFile *File
	var existingContents []byte
	if entry, ok := entries[name]; ok {
		existingFile, ok = entry.(*File)
		if !ok {
			return fmt.Errorf("%s: already added and not a regular file", targetName)
		}
		var err error
		existingContents, err = existingFile.Contents()
		if err != nil {
			return err
		}
	}
	sourceName := ParsedSourceFileName{
		FileName: name,
		Mode:     perm,
		Empty:    empty,
		Template: template,
	}.SourceFileName()
	if parentDirSourceName != "" {
		sourceName = filepath.Join(parentDirSourceName, sourceName)
	}
	file := &File{
		sourceName: sourceName,
		targetName: targetName,
		Empty:      empty,
		Perm:       perm,
		Template:   template,
		contents:   contents,
	}
	if existingFile != nil {
		if bytes.Equal(existingFile.contents, file.contents) {
			if existingFile.sourceName == file.sourceName {
				return nil
			}
			return mutator.Rename(filepath.Join(ts.SourceDir, existingFile.sourceName), filepath.Join(ts.SourceDir, file.sourceName))
		}
		if err := mutator.RemoveAll(filepath.Join(ts.SourceDir, existingFile.sourceName)); err != nil {
			return err
		}
	}
	entries[name] = file
	return mutator.WriteFile(filepath.Join(ts.SourceDir, sourceName), contents, 0666&^ts.Umask, existingContents)
}

func (ts *TargetState) addSymlink(targetName string, entries map[string]Entry, parentDirSourceName string, linkName string, mutator Mutator) error {
	name := filepath.Base(targetName)
	var existingSymlink *Symlink
	var existingLinkName string
	if entry, ok := entries[name]; ok {
		existingSymlink, ok = entry.(*Symlink)
		if !ok {
			return fmt.Errorf("%s: already added and not a symlink", targetName)
		}
		var err error
		existingLinkName, err = existingSymlink.LinkName()
		if err != nil {
			return err
		}
	}
	sourceName := ParsedSourceFileName{
		FileName: name,
		Mode:     os.ModeSymlink,
	}.SourceFileName()
	if parentDirSourceName != "" {
		sourceName = filepath.Join(parentDirSourceName, sourceName)
	}
	symlink := &Symlink{
		sourceName: sourceName,
		targetName: targetName,
		linkName:   linkName,
	}
	if existingSymlink != nil {
		if existingSymlink.linkName == symlink.linkName {
			if existingSymlink.sourceName == symlink.sourceName {
				return nil
			}
			return mutator.Rename(filepath.Join(ts.SourceDir, existingSymlink.sourceName), filepath.Join(ts.SourceDir, symlink.sourceName))
		}
		if err := mutator.RemoveAll(filepath.Join(ts.SourceDir, existingSymlink.sourceName)); err != nil {
			return err
		}
	}
	entries[name] = symlink
	return mutator.WriteFile(filepath.Join(ts.SourceDir, symlink.sourceName), []byte(symlink.linkName), 0666&^ts.Umask, []byte(existingLinkName))
}

func (ts *TargetState) executeTemplate(fs vfs.FS, path string) ([]byte, error) {
	data, err := fs.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ts.executeTemplateData(path, data)
}

func (ts *TargetState) executeTemplateData(name string, data []byte) (_ []byte, err error) {
	tmpl, err := template.New(name).Option("missingkey=error").Funcs(ts.Funcs).Parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %v", name, err)
	}
	defer func() {
		if r := recover(); r != nil {
			if tfe, ok := r.(templateFuncError); ok {
				err = tfe.err
			} else {
				panic(r)
			}
		}
	}()
	output := &bytes.Buffer{}
	if err = tmpl.Execute(output, ts.Data); err != nil {
		return nil, fmt.Errorf("%s: %v", name, err)
	}
	return output.Bytes(), nil
}

func (ts *TargetState) findEntries(dirNames []string) (map[string]Entry, error) {
	entries := ts.Entries
	for i, dirName := range dirNames {
		if entry, ok := entries[dirName]; !ok {
			return nil, os.ErrNotExist
		} else if dir, ok := entry.(*Dir); ok {
			entries = dir.Entries
		} else {
			return nil, fmt.Errorf("%s: not a directory", filepath.Join(dirNames[:i+1]...))
		}
	}
	return entries, nil
}

func (ts *TargetState) findEntry(name string) (Entry, error) {
	names := splitPathList(name)
	entries, err := ts.findEntries(names[:len(names)-1])
	if err != nil {
		return nil, err
	}
	return entries[names[len(names)-1]], nil
}

func (ts *TargetState) importHeader(r io.Reader, header *tar.Header, destinationDir string, stripComponents int, mutator Mutator) error {
	targetPath := header.Name
	if stripComponents > 0 {
		targetPath = filepath.Join(strings.Split(targetPath, string(os.PathSeparator))[stripComponents:]...)
	}
	if destinationDir != "" {
		targetPath = filepath.Join(destinationDir, targetPath)
	} else {
		targetPath = filepath.Join(ts.TargetDir, targetPath)
	}
	targetName, err := filepath.Rel(ts.TargetDir, targetPath)
	if err != nil {
		return err
	}
	parentDirSourceName := ""
	entries := ts.Entries
	if parentDirName := filepath.Dir(targetName); parentDirName != "." {
		parentEntry, err := ts.findEntry(parentDirName)
		if err != nil {
			return err
		}
		parentDir, ok := parentEntry.(*Dir)
		if !ok {
			return fmt.Errorf("%s: parent is not a directory", targetName)
		}
		parentDirSourceName = parentDir.sourceName
		entries = parentDir.Entries
	}
	switch header.Typeflag {
	case tar.TypeDir:
		perm := os.FileMode(header.Mode).Perm()
		empty := false // FIXME don't assume directory is empty
		return ts.addDir(targetName, entries, parentDirSourceName, perm, empty, mutator)
	case tar.TypeReg:
		perm := os.FileMode(header.Mode).Perm()
		empty := header.Size == 0
		contents, err := ioutil.ReadAll(r)
		if err != nil {
			return err
		}
		// FIXME refactor to use tar.Header.FileInfo
		return ts.addFile(targetName, entries, parentDirSourceName, perm, empty, false, contents, mutator)
	case tar.TypeSymlink:
		linkName := header.Linkname
		return ts.addSymlink(targetName, entries, parentDirSourceName, linkName, mutator)
	default:
		return fmt.Errorf("%s: unspported typeflag '%c'", header.Name, header.Typeflag)
	}
}
