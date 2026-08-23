package com.example.app;

import java.util.List;

public class Service {
    private final String name;

    public Service(String name) {
        this.name = name;
    }

    public String getName() {
        return name;
    }

    private void internal() {
    }
}
