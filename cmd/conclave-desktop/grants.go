package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/Emirfs/conclave/internal/grants"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) GrantCatalogue() (grants.Catalogue, error) {
	client, err := a.client()
	if err != nil {
		return grants.Catalogue{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	return client.GrantCatalogue(ctx)
}
func (a *App) RefreshGrantCatalogue() (grants.Catalogue, error) {
	client, err := a.client()
	if err != nil {
		return grants.Catalogue{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 2*time.Minute)
	defer cancel()
	return client.RefreshGrantCatalogue(ctx)
}
func (a *App) AddGrantProgram(input grants.Program) (grants.Catalogue, error) {
	client, err := a.client()
	if err != nil {
		return grants.Catalogue{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	return client.AddGrantProgram(ctx, input)
}
func (a *App) GrantProjects() ([]grants.ProjectSummary, error) {
	client, err := a.client()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	return client.GrantProjects(ctx)
}
func (a *App) CreateGrantProject(input grants.ProjectInput) (grants.Project, error) {
	client, err := a.client()
	if err != nil {
		return grants.Project{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	return client.CreateGrantProject(ctx, input)
}
func (a *App) GrantProject(id int64) (grants.Project, error) {
	client, err := a.client()
	if err != nil {
		return grants.Project{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 60*time.Second)
	defer cancel()
	return client.GrantProject(ctx, id)
}
func (a *App) ApproveGrantSend(id int64, input grants.SendApproval) (grants.Project, error) {
	client, err := a.client()
	if err != nil {
		return grants.Project{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()
	return client.ApproveGrantSend(ctx, id, input)
}
func (a *App) ApproveGrantResearch(id int64, input grants.ResearchApproval) (grants.Project, error) {
	client, err := a.client()
	if err != nil {
		return grants.Project{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()
	return client.ApproveGrantResearch(ctx, id, input)
}
func (a *App) StopGrantProject(id int64) (grants.Project, error) {
	client, err := a.client()
	if err != nil {
		return grants.Project{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()
	return client.StopGrantProject(ctx, id)
}
func (a *App) RetryGrantProject(id int64) (grants.Project, error) {
	client, err := a.client()
	if err != nil {
		return grants.Project{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()
	return client.RetryGrantProject(ctx, id)
}

// Uploaded bytes, not a client-supplied filesystem path. Parsing stays local.
func (a *App) ExtractGrantDocuments(inputs []grants.DocumentInput) []grants.ImportedDocument {
	return grants.ExtractDocuments(inputs)
}

// The daemon supplies the content; clients cannot export an unreviewed draft as final.
func (a *App) ExportGrantDossiers(id int64) ([]string, error) {
	project, err := a.GrantProject(id)
	if err != nil {
		return nil, err
	}
	if project.Stage != "completed" || strings.TrimSpace(project.ApplicationMarkdown) == "" {
		return nil, errors.New("başvuru yalnızca tüm incelemeler tamamlandığında nihai Word olarak dışa aktarılabilir")
	}
	application, err := grants.BuildWord(project.Input.Facts["title"], project.ApplicationMarkdown)
	if err != nil {
		return nil, err
	}
	research, err := grants.BuildWord(project.Input.Facts["title"]+" — AI Çalışma Dosyası", project.ResearchMarkdown)
	if err != nil {
		return nil, err
	}
	folder, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "İki Word dosyasının kaydedileceği klasörü seçin", CanCreateDirectories: true})
	if err != nil || folder == "" {
		return nil, err
	}
	base := grantFilename(project.Input.Facts["title"])
	for suffix := range 100 {
		name := base
		if suffix > 0 {
			name = fmt.Sprintf("%s-%d", base, suffix+1)
		}
		paths := []string{filepath.Join(folder, name+"-basvuru.docx"), filepath.Join(folder, name+"-ai-arastirma.docx")}
		first, err := os.OpenFile(paths[0], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		second, err := os.OpenFile(paths[1], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			first.Close()
			os.Remove(paths[0])
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return nil, err
		}
		if _, err = first.Write(application); err == nil {
			err = first.Close()
		} else {
			first.Close()
		}
		if err == nil {
			_, err = second.Write(research)
		}
		if closeErr := second.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			os.Remove(paths[0])
			os.Remove(paths[1])
			return nil, err
		}
		return paths, nil
	}
	return nil, errors.New("benzersiz dosya adları oluşturulamadı; farklı klasör seçin")
}

func (a *App) ExportGrantResearch(id int64) (string, error) {
	project, err := a.GrantProject(id)
	if err != nil {
		return "", err
	}
	data, err := grants.BuildWord(project.Input.Facts["title"]+" — AI Çalışma Dosyası", grants.BuildResearchRecord(project))
	if err != nil {
		return "", err
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{Title: "AI araştırma ve tartışma dosyasını kaydet", DefaultFilename: grantFilename(project.Input.Facts["title"]) + "-ai-arastirma.docx", Filters: []runtime.FileFilter{{DisplayName: "Word belgesi", Pattern: "*.docx"}}})
	if err != nil || path == "" {
		return "", err
	}
	if _, err := os.Stat(path); err == nil {
		answer, err := runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{Type: runtime.QuestionDialog, Title: "Var olan dosya", Message: path + " dosyasının üzerine yazılsın mı?", Buttons: []string{"Üzerine yaz", "İptal"}, DefaultButton: "İptal", CancelButton: "İptal"})
		if err != nil || answer != "Üzerine yaz" {
			return "", err
		}
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func grantFilename(title string) string {
	var name strings.Builder
	for _, character := range title {
		if unicode.IsLetter(character) || unicode.IsDigit(character) || character == '-' || character == '_' {
			name.WriteRune(character)
		} else if character == ' ' {
			name.WriteByte('-')
		}
		if name.Len() > 100 {
			break
		}
	}
	if name.Len() == 0 {
		return "basvuru"
	}
	return name.String()
}
