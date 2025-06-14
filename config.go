package main

import (
        "io/ioutil"

        "gopkg.in/yaml.v2"
)

type Config struct {
        Matrix struct {
                ServerURL  string `yaml:"server_url"`
                User       string `yaml:"user"`
                Password   string `yaml:"password"`
                RoomID     string `yaml:"room_id"`
                TokenFile  string `yaml:"token_file"`
        } `yaml:"matrix"`
        Wallhaven struct {
                APIToken   string `yaml:"api_token"`
                Categories string `yaml:"categories"`
                Purity     string `yaml:"purity"`
                Sorting    string `yaml:"sorting"`
                Toprange   []string `yaml:"toprange"`
                Order      string `yaml:"order"`
                AIFilter   string `yaml:"ai_filter"`
                UserAgent  string `yaml:"user_agent"`
        } `yaml:"wallhaven"`
        Database string `yaml:"database"`
        WaitTime int    `yaml:"wait_time"`
        OpenAIKey string `yaml:"openai_key"`
        Mastodon struct {
            Server      string `yaml:"mastodon_server"`
            AccessToken string `yaml:"mastodon_token"`
        }
        Ntfy struct {
            Server string `yaml:"server"`
            Topic  string `yaml:"topic"`
        } `yaml:"ntfy"`
}

func LoadConfig(filename string) (*Config, error) {
        data, err := ioutil.ReadFile(filename)
        if err != nil {
                return nil, err
        }
        var cfg Config
        if err := yaml.Unmarshal(data, &cfg); err != nil {
                return nil, err
        }
        return &cfg, nil
}
