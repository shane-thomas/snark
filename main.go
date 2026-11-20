package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"

	"github.com/joho/godotenv"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"
	"google.golang.org/genai"
)

type App struct {
	config       *oauth2.Config
	token        *oauth2.Token
	geminiClient *genai.Client
}

type Playlist struct {
	ID          string
	Title       string
	Description string
	Thumbnail   string
}

type Channel struct {
	ID          string
	Title       string
	Description string
	Thumbnail   string
}

var index = template.Must(template.ParseFiles("templates/index.html"))

func main() {

	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found; using environment variables")
	}
	geminiClient, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  os.Getenv("GEMINI_API_KEY"),
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		log.Fatal("Failed to initialize Gemini:", err)
	}
	var config = &oauth2.Config{
		ClientID:     os.Getenv("CLIENT_ID"),
		ClientSecret: os.Getenv("CLIENT_SECRET"),
		Endpoint:     google.Endpoint,
		Scopes: []string{
			youtube.YoutubeReadonlyScope,
		},
		RedirectURL: os.Getenv("GOOGLE_REDIRECT_URL"),
		// RedirectURL: "http://localhost:8000/auth/google/callback",
	}

	app := &App{config: config, geminiClient: geminiClient}

	http.HandleFunc("GET /{$}", HomepageHandler)
	http.HandleFunc("GET /auth/google", app.GoogleSignInHandler)

	http.HandleFunc("GET /auth/google/callback", app.GoogleCallbackHandler)
	http.HandleFunc("GET /dashboard", app.DashboardHandler)
	fmt.Println("Server listening on http://localhost:8000")
	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}

	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func HomepageHandler(w http.ResponseWriter, r *http.Request) {
	// w.Write([]byte("Hello!"))
	err := index.Execute(w, nil)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		log.Println("Template execution error: ", err)
	}
}

func (a *App) GoogleSignInHandler(w http.ResponseWriter, r *http.Request) {
	state, err := generateState()
	if err != nil {
		http.Error(w, "Something went wrong!", http.StatusInternalServerError)
		log.Println("State generation error:", err)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   300,
	})
	log.Println("OAuth sign-in host:", r.Host)
	log.Println("OAuth state cookie set")
	url := a.config.AuthCodeURL(state)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

func (a *App) GoogleCallbackHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("OAuth callback host:", r.Host)

	cookie, err := r.Cookie("oauth_state")
	if err != nil {
		log.Println("OAuth state cookie missing:", err)
	} else {
		log.Println("OAuth state cookie received")
	}
	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		http.Error(w, "Google authorization failed: "+errMsg, http.StatusUnauthorized)
		return
	}
	returnedState := r.URL.Query().Get("state")

	// cookie, err := r.Cookie("oauth_state")
	if err != nil {
		log.Println("Invalid OAuth state: ", err)
		http.Error(w, "Invalid OAuth state", http.StatusBadRequest)
		return
	}

	if returnedState != cookie.Value {
		http.Error(w, "Invalid OAuth state", http.StatusBadRequest)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})

	code := r.URL.Query().Get("code")

	if code == "" {
		http.Error(w, "Missing authorization code", http.StatusBadRequest)
		return
	}

	token, err := a.config.Exchange(r.Context(), code)
	if err != nil {
		log.Println("Token exchange error:", err)
		http.Error(w, "Failed to authenticate with Google", http.StatusInternalServerError)
		return
	}

	a.token = token

	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func generateState() (string, error) {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}

	return hex.EncodeToString(b), nil

}

func (a *App) YouTubeService(ctx context.Context) (*youtube.Service, error) {
	service, err := youtube.NewService(ctx, option.WithTokenSource(a.config.TokenSource(ctx, a.token)))
	if err != nil {
		return nil, err
	}
	return service, nil
}
func (a *App) DashboardHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Dashboard request started")

	if a.token == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	profile, err := a.GetYouTubeProfile(r.Context())

	log.Println("Dashboard context status:", r.Context().Err())

	if err != nil {
		log.Println("Profile fetching error:", err)
		http.Error(w, "Failed to fetch YouTube profile", http.StatusInternalServerError)
		return
	}

	log.Println("Profile fetched successfully")

	roastInput := profile.ToRoastInput()

	roast, err := a.GenerateRoast(r.Context(), roastInput)
	if err != nil {
		log.Println("Gemini error:", err)
		http.Error(w, "Failed to generate roast", http.StatusInternalServerError)
		return
	}

	fmt.Fprintln(w, roast)
}
func (a *App) GetChannel(ctx context.Context) (*Channel, error) {
	service, err := a.YouTubeService(ctx)
	if err != nil {
		return nil, err
	}

	response, err := service.Channels.List([]string{"snippet"}).
		Mine(true).
		Context(ctx).
		Do()
	if err != nil {
		return nil, err
	}

	if len(response.Items) == 0 {
		return nil, fmt.Errorf("no YouTube channel found")
	}

	snippet := response.Items[0].Snippet

	return &Channel{
		ID:          response.Items[0].Id,
		Title:       snippet.Title,
		Description: snippet.Description,
		Thumbnail:   snippet.Thumbnails.Default.Url,
	}, nil
}

func (a *App) GetPlaylists(ctx context.Context) ([]Playlist, error) {
	service, err := a.YouTubeService(ctx)
	if err != nil {
		return nil, err
	}

	var playlists []Playlist
	pageToken := ""

	for {
		response, err := service.Playlists.List([]string{"snippet"}).
			Mine(true).
			MaxResults(50).
			PageToken(pageToken).
			Context(ctx).
			Do()

		if err != nil {
			return nil, err
		}

		for _, item := range response.Items {
			playlists = append(playlists, Playlist{
				ID:          item.Id,
				Title:       item.Snippet.Title,
				Description: item.Snippet.Description,
				Thumbnail:   item.Snippet.Thumbnails.Default.Url,
			})
		}

		if response.NextPageToken == "" {
			break
		}

		pageToken = response.NextPageToken
	}

	return playlists, nil
}

type Video struct {
	ID           string
	Title        string
	Description  string
	Thumbnail    string
	ChannelTitle string
}

func (a *App) GetLikedVideos(ctx context.Context) ([]Video, error) {
	service, err := a.YouTubeService(ctx)
	if err != nil {
		return nil, err
	}

	var videos []Video
	pageToken := ""

	for {
		response, err := service.Videos.List([]string{"snippet"}).
			MyRating("like").
			MaxResults(50).
			PageToken(pageToken).
			Context(ctx).
			Do()

		if err != nil {
			return nil, err
		}

		for _, item := range response.Items {
			videos = append(videos, Video{
				Title:        item.Snippet.Title,
				ChannelTitle: item.Snippet.ChannelTitle,
			})
		}

		if response.NextPageToken == "" {
			break
		}

		pageToken = response.NextPageToken
	}

	return videos, nil
}

type Subscription struct {
	ID                 string
	ChannelID          string
	ChannelTitle       string
	ChannelDescription string
	Thumbnail          string
}

func (a *App) GetSubscriptions(ctx context.Context) ([]Subscription, error) {
	service, err := a.YouTubeService(ctx)
	if err != nil {
		return nil, err
	}

	var subscriptions []Subscription
	pageToken := ""

	for {
		response, err := service.Subscriptions.List([]string{"snippet"}).
			Mine(true).
			MaxResults(50).
			PageToken(pageToken).
			Context(ctx).
			Do()

		if err != nil {
			return nil, err
		}

		for _, item := range response.Items {
			subscriptions = append(subscriptions, Subscription{
				ID:                 item.Id,
				ChannelID:          item.Snippet.ResourceId.ChannelId,
				ChannelTitle:       item.Snippet.Title,
				ChannelDescription: item.Snippet.Description,
				Thumbnail:          item.Snippet.Thumbnails.Default.Url,
			})
		}

		if response.NextPageToken == "" {
			break
		}

		pageToken = response.NextPageToken
	}

	return subscriptions, nil
}

type YouTubeProfile struct {
	Channel       *Channel
	Playlists     []Playlist
	LikedVideos   []Video
	Subscriptions []Subscription
}

func (a *App) GetYouTubeProfile(ctx context.Context) (*YouTubeProfile, error) {
	log.Println("Fetching channel...")
	channel, err := a.GetChannel(ctx)
	if err != nil {
		return nil, fmt.Errorf("get channel: %w", err)
	}

	log.Println("Fetching playlists...")
	playlists, err := a.GetPlaylists(ctx)
	if err != nil {
		return nil, fmt.Errorf("get playlists: %w", err)
	}

	log.Println("Fetching liked videos...")
	likedVideos, err := a.GetLikedVideos(ctx)
	if err != nil {
		return nil, fmt.Errorf("get liked videos: %w", err)
	}

	log.Println("Fetching subscriptions...")
	subscriptions, err := a.GetSubscriptions(ctx)
	if err != nil {
		return nil, fmt.Errorf("get subscriptions: %w", err)
	}

	return &YouTubeProfile{
		Channel:       channel,
		Playlists:     playlists,
		LikedVideos:   likedVideos,
		Subscriptions: subscriptions,
	}, nil
}

type RoastInput struct {
	ChannelTitle       string
	ChannelDescription string
	PlaylistTitles     []string
	LikedVideos        []Video
	Subscriptions      []string
	PlaylistCount      int
	LikedVideoCount    int
	SubscriptionCount  int
}

func (p *YouTubeProfile) ToRoastInput() *RoastInput {
	input := &RoastInput{
		ChannelTitle:       p.Channel.Title,
		ChannelDescription: p.Channel.Description,
		PlaylistTitles:     make([]string, 0, len(p.Playlists)),
		LikedVideos:        p.LikedVideos,
		Subscriptions:      make([]string, 0, len(p.Subscriptions)),
		PlaylistCount:      len(p.Playlists),
		LikedVideoCount:    len(p.LikedVideos),
		SubscriptionCount:  len(p.Subscriptions),
	}

	for _, playlist := range p.Playlists {
		input.PlaylistTitles = append(input.PlaylistTitles, playlist.Title)
	}

	for _, subscription := range p.Subscriptions {
		input.Subscriptions = append(input.Subscriptions, subscription.ChannelTitle)
	}

	return input
}

func (a *App) GenerateRoast(ctx context.Context, input *RoastInput) (string, error) {
	data, err := json.Marshal(input)
	if err != nil {
		return "", err
	}

	prompt := `You are Snark, a witty and brutally funny YouTube taste critic.

Analyze the user's YouTube profile and roast their viewing habits, interests, and subscriptions.

Be specific. Reference actual videos, channels, and playlists.
Avoid generic jokes. Make it entertaining and personal.

Here is the user's YouTube profile:

` + string(data)

	response, err := a.geminiClient.Models.GenerateContent(
		ctx,
		"gemini-3.5-flash-lite",
		genai.Text(prompt),
		nil,
	)
	if err != nil {
		return "", err
	}

	return response.Text(), nil
}
